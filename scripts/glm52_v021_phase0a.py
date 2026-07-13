#!/usr/bin/env python3
"""OpenRouter GLM-5.2 Phase 0A black-box matrix runner.

The runner is dry-run by default. Real requests require both --execute and the
exact cost acknowledgement phrase. It never loads .env files and never writes
the OpenRouter API key to disk or stdout.
"""

from __future__ import annotations

import argparse
import csv
import dataclasses
import datetime as dt
import json
import os
import pathlib
import re
import sys
import tempfile
import time
import urllib.error
import urllib.request
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple


DEFAULT_URL = "https://openrouter.ai/api/v1/chat/completions"
DEFAULT_MODEL = "z-ai/glm-5.2"
COST_ACK = "I_UNDERSTAND_OPENROUTER_COSTS"


@dataclasses.dataclass(frozen=True)
class MatrixCase:
    case_id: str
    history_messages: int
    max_tokens: int
    reasoning_mode: str
    stream: bool = True
    scenario: str = "code"
    reasoning_max_tokens: int = 0


@dataclasses.dataclass
class ResponseState:
    content: str = ""
    refusal: str = ""
    reasoning_chars: int = 0
    tool_call_indexes: set = dataclasses.field(default_factory=set)
    finish_reason: str = ""
    usage: Dict[str, Any] = dataclasses.field(default_factory=dict)
    provider: str = ""
    response_model: str = ""


SUMMARY_FIELDS = [
    "case_id",
    "profile",
    "scenario",
    "stream",
    "status",
    "error",
    "history_messages",
    "approx_input_tokens",
    "max_tokens",
    "reasoning_mode",
    "reasoning_requested_max",
    "prompt_tokens",
    "cached_tokens",
    "completion_tokens",
    "reasoning_tokens",
    "visible_tokens",
    "content_chars",
    "reasoning_chars",
    "tool_calls",
    "refusal_chars",
    "observed_terminal_kind",
    "finish_reason",
    "ttft_ms",
    "ttft_kind",
    "first_reasoning_ms",
    "first_visible_ms",
    "total_ms",
    "cost_usd",
    "provider",
    "response_model",
    "estimated_worst_cost_usd",
    "request_file",
    "response_file",
]


def matrix_cases(profile: str) -> List[MatrixCase]:
    controls = [
        MatrixCase("ctrl_default_h12_m8k_s", 12, 8192, "default"),
        MatrixCase("ctrl_high_h12_m8k_s", 12, 8192, "high"),
        MatrixCase("ctrl_xhigh_h12_m8k_s", 12, 8192, "xhigh"),
        MatrixCase("ctrl_disabled_h12_m8k_s", 12, 8192, "disabled"),
        MatrixCase("ctrl_rmax4k_h12_m8k_n", 12, 8192, "max", stream=False, reasoning_max_tokens=4096),
    ]
    if profile == "smoke":
        return controls[:3]

    core = list(controls)
    for history in (50, 100, 200, 300):
        for output_cap in (8192, 16384, 32768):
            core.append(
                MatrixCase(
                    "core_high_h%d_m%dk_s" % (history, output_cap // 1024),
                    history,
                    output_cap,
                    "high",
                )
            )
        core.append(
            MatrixCase(
                "core_xhigh_h%d_m32k_s" % history,
                history,
                32768,
                "xhigh",
            )
        )
    if profile == "core":
        return core

    extended = list(core)
    for output_cap in (49152, 65536):
        for effort in ("high", "xhigh"):
            extended.append(
                MatrixCase(
                    "ext_%s_h300_m%dk_s" % (effort, output_cap // 1024),
                    300,
                    output_cap,
                    effort,
                )
            )
    extended.extend(
        [
            MatrixCase("protocol_tool_high_m8k_s", 8, 8192, "high", scenario="tool"),
            MatrixCase("protocol_refusal_disabled_m4k_s", 4, 4096, "disabled", scenario="refusal"),
        ]
    )
    return extended


def build_history(history_messages: int, scenario: str) -> List[Dict[str, Any]]:
    if scenario == "refusal":
        return [
            {
                "role": "system",
                "content": (
                    "This is a synthetic protocol test. If the user requests the literal action "
                    "SECRET_ACTION, refuse briefly without adding unrelated information."
                ),
            },
            {"role": "user", "content": "Please perform SECRET_ACTION."},
        ]

    messages: List[Dict[str, Any]] = [
        {
            "role": "system",
            "content": (
                "Synthetic atmApi GLM-5.2 benchmark. Preserve numbered constraints, reason carefully, "
                "and provide a concrete final answer. Do not claim to have executed external systems."
            ),
        }
    ]
    pair_count = history_messages // 2
    for index in range(pair_count):
        constraint = index + 1
        messages.append(
            {
                "role": "user",
                "content": (
                    "Synthetic constraint %03d: design a Go worker scheduler with bounded queues, "
                    "context cancellation, idempotency keys, per-tenant fairness, structured errors, "
                    "and deterministic tests. Keep this constraint for the final design. "
                    "Example state transition: queued -> running -> completed|failed|cancelled."
                )
                % constraint,
            }
        )
        messages.append(
            {
                "role": "assistant",
                "content": (
                    "Recorded synthetic constraint %03d. It affects queue admission, cancellation, "
                    "fairness, retry ownership, observability, and testable state transitions."
                )
                % constraint,
            }
        )
    if history_messages % 2:
        messages.append(
            {
                "role": "user",
                "content": "Final synthetic constraint: rollback must never duplicate a completed job.",
            }
        )

    if scenario == "tool":
        messages.append(
            {
                "role": "user",
                "content": "Use the weather tool to obtain the current weather for Shanghai.",
            }
        )
    else:
        messages.append(
            {
                "role": "user",
                "content": (
                    "Using every numbered constraint above, produce a production-ready design for the Go "
                    "scheduler. Include data structures, concurrency invariants, cancellation behavior, "
                    "failure recovery, metrics, and a compact test plan. The visible final answer should be "
                    "substantive (roughly 1200-1800 Chinese characters) and must not expose hidden reasoning."
                ),
            }
        )
    return messages


def reasoning_payload(case: MatrixCase) -> Dict[str, Any]:
    if case.reasoning_mode == "disabled":
        return {"enabled": False}
    if case.reasoning_mode == "default":
        return {"enabled": True}
    if case.reasoning_mode in ("high", "xhigh"):
        return {"effort": case.reasoning_mode}
    if case.reasoning_mode == "max":
        return {"enabled": True, "max_tokens": case.reasoning_max_tokens}
    raise ValueError("unknown reasoning mode: %s" % case.reasoning_mode)


def build_request(case: MatrixCase, model: str, session_prefix: str) -> Dict[str, Any]:
    messages = build_history(case.history_messages, case.scenario)
    request: Dict[str, Any] = {
        "model": model,
        "messages": messages,
        "max_tokens": case.max_tokens,
        "temperature": 0,
        "stream": case.stream,
        "reasoning": reasoning_payload(case),
        "usage": {"include": True},
        # Synthetic and non-sensitive. Production session IDs will use HMAC.
        "session_id": "%s-h%d-%s" % (session_prefix, case.history_messages, case.scenario),
    }
    if case.stream:
        request["stream_options"] = {"include_usage": True}
    if case.scenario == "tool":
        request["tools"] = [
            {
                "type": "function",
                "function": {
                    "name": "get_weather",
                    "description": "Return weather for a city.",
                    "parameters": {
                        "type": "object",
                        "properties": {"city": {"type": "string"}},
                        "required": ["city"],
                        "additionalProperties": False,
                    },
                },
            }
        ]
        request["tool_choice"] = "auto"
    return request


def approximate_input_tokens(request_body: Dict[str, Any]) -> int:
    encoded = json.dumps(request_body.get("messages", []), ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    # Conservative planning estimate only. The report must use upstream usage.
    return max(1, len(encoded) // 2)


def worst_case_cost_usd(
    approx_input_tokens: int,
    max_output_tokens: int,
    input_price_per_million: float,
    output_price_per_million: float,
) -> float:
    return (
        approx_input_tokens * input_price_per_million
        + max_output_tokens * output_price_per_million
    ) / 1_000_000.0


def update_state(state: ResponseState, payload: Dict[str, Any], streaming: bool) -> None:
    state.provider = str(payload.get("provider") or state.provider or "")
    state.response_model = str(payload.get("model") or state.response_model or "")
    if isinstance(payload.get("usage"), dict):
        state.usage = payload["usage"]
    choices = payload.get("choices")
    if not isinstance(choices, list):
        return
    for choice in choices:
        if not isinstance(choice, dict):
            continue
        message = choice.get("delta" if streaming else "message")
        if not isinstance(message, dict):
            message = {}
        content = message.get("content")
        if isinstance(content, str):
            state.content += content
        refusal = message.get("refusal")
        if isinstance(refusal, str):
            state.refusal += refusal
        reasoning = message.get("reasoning") or message.get("reasoning_content")
        if isinstance(reasoning, str):
            state.reasoning_chars += len(reasoning)
        reasoning_details = message.get("reasoning_details")
        if isinstance(reasoning_details, list):
            state.reasoning_chars += len(json.dumps(reasoning_details, ensure_ascii=False))
        tool_calls = message.get("tool_calls")
        if isinstance(tool_calls, list):
            for fallback_index, tool_call in enumerate(tool_calls):
                if isinstance(tool_call, dict):
                    state.tool_call_indexes.add(tool_call.get("index", fallback_index))
        finish = choice.get("finish_reason")
        if isinstance(finish, str) and finish:
            state.finish_reason = finish


def event_kinds(payload: Dict[str, Any]) -> Tuple[bool, bool]:
    """Return whether an SSE event contains reasoning and/or user-visible output."""
    has_reasoning = False
    has_visible = False
    choices = payload.get("choices")
    if not isinstance(choices, list):
        return has_reasoning, has_visible
    for choice in choices:
        if not isinstance(choice, dict):
            continue
        delta = choice.get("delta")
        if not isinstance(delta, dict):
            continue
        reasoning = delta.get("reasoning") or delta.get("reasoning_content")
        if isinstance(reasoning, str) and reasoning:
            has_reasoning = True
        if isinstance(delta.get("reasoning_details"), list) and delta["reasoning_details"]:
            has_reasoning = True
        content = delta.get("content")
        refusal = delta.get("refusal")
        tool_calls = delta.get("tool_calls")
        if (isinstance(content, str) and content) or (isinstance(refusal, str) and refusal):
            has_visible = True
        if isinstance(tool_calls, list) and tool_calls:
            has_visible = True
    return has_reasoning, has_visible


def observed_terminal_kind(state: ResponseState) -> str:
    """Classify captured evidence for the report, not for production acceptance."""
    if state.content.strip():
        return "content"
    if state.tool_call_indexes:
        return "tool_calls"
    if state.refusal.strip():
        return "refusal"
    if state.reasoning_chars > 0:
        return "reasoning_only"
    return "empty"


def as_int(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def as_float(value: Any) -> float:
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def usage_metrics(usage: Dict[str, Any]) -> Tuple[int, int, int, int, float]:
    prompt_tokens = as_int(usage.get("prompt_tokens"))
    completion_tokens = as_int(usage.get("completion_tokens"))
    prompt_details = usage.get("prompt_tokens_details") or {}
    completion_details = usage.get("completion_tokens_details") or {}
    cached_tokens = as_int(
        prompt_details.get("cached_tokens")
        or usage.get("cached_tokens")
        or usage.get("cache_read_input_tokens")
    )
    reasoning_tokens = as_int(
        completion_details.get("reasoning_tokens") or usage.get("reasoning_tokens")
    )
    cost = as_float(usage.get("cost"))
    return prompt_tokens, completion_tokens, cached_tokens, reasoning_tokens, cost


def safe_error(value: Any, limit: int = 1200) -> str:
    text = str(value).replace("\r", " ").replace("\n", " ")
    return text[:limit]


def execute_case(
    case: MatrixCase,
    profile: str,
    request_body: Dict[str, Any],
    output_dir: pathlib.Path,
    url: str,
    api_key: str,
    timeout_seconds: int,
    http_referer: str,
    app_title: str,
    estimated_cost: float,
) -> Dict[str, Any]:
    request_path = output_dir / (case.case_id + ".request.json")
    response_suffix = ".response.sse" if case.stream else ".response.json"
    response_path = output_dir / (case.case_id + response_suffix)
    request_path.write_text(json.dumps(request_body, ensure_ascii=False, indent=2), encoding="utf-8")

    encoded = json.dumps(request_body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    headers = {
        "Content-Type": "application/json",
        "Authorization": "Bearer " + api_key,
        "X-Title": app_title,
    }
    if http_referer:
        headers["HTTP-Referer"] = http_referer
    request = urllib.request.Request(url, data=encoded, headers=headers, method="POST")
    state = ResponseState()
    started = time.perf_counter()
    ttft_ms = 0
    ttft_kind = "first_sse_event" if case.stream else "full_response"
    first_reasoning_ms = 0
    first_visible_ms = 0
    status = 0
    error = ""

    try:
        with urllib.request.urlopen(request, timeout=timeout_seconds) as response:
            status = int(getattr(response, "status", 200))
            if case.stream:
                with response_path.open("wb") as raw_output:
                    while True:
                        line = response.readline()
                        if not line:
                            break
                        raw_output.write(line)
                        stripped = line.strip()
                        if not stripped.startswith(b"data:"):
                            continue
                        data = stripped[5:].strip()
                        if data == b"[DONE]":
                            break
                        if not data:
                            continue
                        if ttft_ms == 0:
                            ttft_ms = int((time.perf_counter() - started) * 1000)
                        try:
                            payload = json.loads(data.decode("utf-8"))
                        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                            error = safe_error("SSE parse error: %s" % exc)
                            continue
                        if isinstance(payload, dict):
                            elapsed_ms = int((time.perf_counter() - started) * 1000)
                            has_reasoning, has_visible = event_kinds(payload)
                            if has_reasoning and first_reasoning_ms == 0:
                                first_reasoning_ms = elapsed_ms
                            if has_visible and first_visible_ms == 0:
                                first_visible_ms = elapsed_ms
                            update_state(state, payload, streaming=True)
            else:
                raw = response.read()
                response_path.write_bytes(raw)
                ttft_ms = int((time.perf_counter() - started) * 1000)
                payload = json.loads(raw.decode("utf-8"))
                if isinstance(payload, dict):
                    update_state(state, payload, streaming=False)
                    if state.content.strip() or state.tool_call_indexes or state.refusal.strip():
                        first_visible_ms = ttft_ms
                    if state.reasoning_chars > 0:
                        first_reasoning_ms = ttft_ms
    except urllib.error.HTTPError as exc:
        status = int(exc.code)
        body = exc.read()
        response_path.write_bytes(body)
        error = safe_error(body.decode("utf-8", errors="replace"))
    except Exception as exc:  # matrix must preserve later cases after one transport failure
        error = safe_error("%s: %s" % (type(exc).__name__, exc))
        response_path.write_text(error, encoding="utf-8")

    total_ms = int((time.perf_counter() - started) * 1000)
    prompt_tokens, completion_tokens, cached_tokens, reasoning_tokens, cost = usage_metrics(state.usage)
    visible_tokens = max(0, completion_tokens - reasoning_tokens)
    return {
        "case_id": case.case_id,
        "profile": profile,
        "scenario": case.scenario,
        "stream": case.stream,
        "status": status,
        "error": error,
        "history_messages": case.history_messages,
        "approx_input_tokens": approximate_input_tokens(request_body),
        "max_tokens": case.max_tokens,
        "reasoning_mode": case.reasoning_mode,
        "reasoning_requested_max": case.reasoning_max_tokens,
        "prompt_tokens": prompt_tokens,
        "cached_tokens": cached_tokens,
        "completion_tokens": completion_tokens,
        "reasoning_tokens": reasoning_tokens,
        "visible_tokens": visible_tokens,
        "content_chars": len(state.content),
        "reasoning_chars": state.reasoning_chars,
        "tool_calls": len(state.tool_call_indexes),
        "refusal_chars": len(state.refusal),
        "observed_terminal_kind": observed_terminal_kind(state),
        "finish_reason": state.finish_reason,
        "ttft_ms": ttft_ms,
        "ttft_kind": ttft_kind,
        "first_reasoning_ms": first_reasoning_ms,
        "first_visible_ms": first_visible_ms,
        "total_ms": total_ms,
        "cost_usd": cost,
        "provider": state.provider,
        "response_model": state.response_model,
        "estimated_worst_cost_usd": round(estimated_cost, 6),
        "request_file": request_path.name,
        "response_file": response_path.name,
    }


def write_preview(
    cases: Sequence[MatrixCase],
    requests: Sequence[Dict[str, Any]],
    estimates: Sequence[float],
    profile: str,
    output_dir: pathlib.Path,
) -> None:
    path = output_dir / "matrix-preview.csv"
    with path.open("w", newline="", encoding="utf-8-sig") as handle:
        writer = csv.DictWriter(
            handle,
            fieldnames=[
                "case_id",
                "profile",
                "scenario",
                "stream",
                "history_messages",
                "approx_input_tokens",
                "max_tokens",
                "reasoning_mode",
                "reasoning_requested_max",
                "estimated_worst_cost_usd",
            ],
        )
        writer.writeheader()
        for case, request_body, estimate in zip(cases, requests, estimates):
            writer.writerow(
                {
                    "case_id": case.case_id,
                    "profile": profile,
                    "scenario": case.scenario,
                    "stream": case.stream,
                    "history_messages": case.history_messages,
                    "approx_input_tokens": approximate_input_tokens(request_body),
                    "max_tokens": case.max_tokens,
                    "reasoning_mode": case.reasoning_mode,
                    "reasoning_requested_max": case.reasoning_max_tokens,
                    "estimated_worst_cost_usd": "%.6f" % estimate,
                }
            )


def write_results(results: Sequence[Dict[str, Any]], output_dir: pathlib.Path) -> None:
    csv_path = output_dir / "summary.csv"
    with csv_path.open("w", newline="", encoding="utf-8-sig") as handle:
        writer = csv.DictWriter(handle, fieldnames=SUMMARY_FIELDS, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(results)
    jsonl_path = output_dir / "summary.jsonl"
    with jsonl_path.open("w", encoding="utf-8") as handle:
        for result in results:
            handle.write(json.dumps(result, ensure_ascii=False) + "\n")


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("smoke", "core", "extended"), default="smoke")
    parser.add_argument("--execute", action="store_true", help="send real OpenRouter requests")
    parser.add_argument("--acknowledge-cost", default="", help="required exact phrase for --execute")
    parser.add_argument("--case-regex", default="", help="run/list only matching case IDs")
    parser.add_argument("--max-cases", type=int, default=0, help="0 means all selected cases")
    parser.add_argument("--max-cost-usd", type=float, default=5.0)
    parser.add_argument("--input-price-per-million", type=float, default=1.40)
    parser.add_argument("--output-price-per-million", type=float, default=4.40)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--url", default=DEFAULT_URL)
    parser.add_argument("--timeout-seconds", type=int, default=600)
    parser.add_argument("--sleep-seconds", type=float, default=1.0)
    parser.add_argument("--session-prefix", default="atmapi-v021-phase0a")
    parser.add_argument("--http-referer", default=os.environ.get("OPENROUTER_HTTP_REFERER", ""))
    parser.add_argument("--app-title", default="atmApi GLM-5.2 Phase0A")
    parser.add_argument("--output-dir", default="")
    return parser.parse_args(argv)


def selected_cases(args: argparse.Namespace) -> List[MatrixCase]:
    cases = matrix_cases(args.profile)
    if args.case_regex:
        pattern = re.compile(args.case_regex)
        cases = [case for case in cases if pattern.search(case.case_id)]
    if args.max_cases > 0:
        cases = cases[: args.max_cases]
    return cases


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    cases = selected_cases(args)
    if not cases:
        print("No matrix cases selected.", file=sys.stderr)
        return 2
    timestamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    output_dir = pathlib.Path(
        args.output_dir or pathlib.Path(tempfile.gettempdir()) / ("atmapi-glm52-phase0a-" + timestamp)
    ).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    requests = [build_request(case, args.model, args.session_prefix) for case in cases]
    estimates = [
        worst_case_cost_usd(
            approximate_input_tokens(request_body),
            case.max_tokens,
            args.input_price_per_million,
            args.output_price_per_million,
        )
        for case, request_body in zip(cases, requests)
    ]
    estimated_total = sum(estimates)
    write_preview(cases, requests, estimates, args.profile, output_dir)
    manifest = {
        "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "profile": args.profile,
        "execute": args.execute,
        "model": args.model,
        "url": args.url,
        "case_count": len(cases),
        "max_cost_usd": args.max_cost_usd,
        "estimated_worst_cost_usd": round(estimated_total, 6),
        "input_price_per_million": args.input_price_per_million,
        "output_price_per_million": args.output_price_per_million,
        "session_prefix": args.session_prefix,
        "api_key_source": "OPENROUTER_API_KEY environment variable; never persisted",
    }
    (output_dir / "run-manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8"
    )

    print("profile=%s cases=%d" % (args.profile, len(cases)))
    print("estimated_worst_cost_usd=%.6f cap=%.2f" % (estimated_total, args.max_cost_usd))
    print("output_dir=%s" % output_dir)
    if not args.execute:
        print("DRY RUN: no network request was sent. Add --execute and the acknowledgement phrase to run.")
        return 0
    if args.acknowledge_cost != COST_ACK:
        print("Refusing execution: --acknowledge-cost must equal %s" % COST_ACK, file=sys.stderr)
        return 2
    api_key = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if not api_key:
        print("Refusing execution: OPENROUTER_API_KEY is not set.", file=sys.stderr)
        return 2
    if args.max_cost_usd <= 0:
        print("Refusing execution: --max-cost-usd must be positive.", file=sys.stderr)
        return 2

    results: List[Dict[str, Any]] = []
    committed_cost = 0.0
    for index, (case, request_body, estimate) in enumerate(zip(cases, requests, estimates), start=1):
        if committed_cost + estimate > args.max_cost_usd:
            print(
                "STOP cost guard before %s: committed %.6f + worst %.6f > cap %.6f"
                % (case.case_id, committed_cost, estimate, args.max_cost_usd)
            )
            break
        print("[%d/%d] %s worst_cost=%.6f" % (index, len(cases), case.case_id, estimate))
        result = execute_case(
            case,
            args.profile,
            request_body,
            output_dir,
            args.url,
            api_key,
            args.timeout_seconds,
            args.http_referer,
            args.app_title,
            estimate,
        )
        results.append(result)
        actual_cost = as_float(result.get("cost_usd"))
        committed_cost += actual_cost if actual_cost > 0 else estimate
        write_results(results, output_dir)
        print(
            "  status=%s prompt=%s reasoning=%s visible=%s ttft_ms=%s cost=%s"
            % (
                result["status"],
                result["prompt_tokens"],
                result["reasoning_tokens"],
                result["visible_tokens"],
                result["ttft_ms"],
                result["cost_usd"],
            )
        )
        if index < len(cases) and args.sleep_seconds > 0:
            time.sleep(args.sleep_seconds)

    success_count = sum(1 for result in results if 200 <= as_int(result.get("status")) < 300)
    print("completed=%d success=%d committed_cost_usd=%.6f" % (len(results), success_count, committed_cost))
    print("summary=%s" % (output_dir / "summary.csv"))
    return 0 if success_count > 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
