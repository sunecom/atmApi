#!/usr/bin/env python3
import importlib.util
import pathlib
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).with_name("glm52_v021_phase0a.py")
SPEC = importlib.util.spec_from_file_location("glm52_v021_phase0a", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class FakeHTTPResponse:
    status = 200

    def __init__(self, lines):
        self.lines = iter(lines)

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def readline(self):
        return next(self.lines, b"")


class Phase0ATest(unittest.TestCase):
    def test_profiles_have_unique_case_ids(self):
        for profile, expected_count in (
            ("smoke", 3),
            ("core", 32),
            ("core-safe", 32),
            ("extended", 36),
        ):
            cases = MODULE.matrix_cases(profile)
            self.assertEqual(expected_count, len(cases))
            self.assertEqual(len(cases), len({case.case_id for case in cases}))

    def test_core_safe_pairs_default_and_high_without_xhigh(self):
        cases = MODULE.matrix_cases("core-safe")
        self.assertEqual(cases, MODULE.matrix_cases("core"))
        self.assertNotIn("xhigh", {case.reasoning_mode for case in cases})
        self.assertNotIn(
            "xhigh",
            {case.reasoning_mode for case in MODULE.matrix_cases("extended")},
        )
        for history in (50, 100, 200, 300):
            for output_cap in (8192, 16384, 32768):
                efforts = {
                    case.reasoning_mode
                    for case in cases
                    if case.history_messages == history
                    and case.max_tokens == output_cap
                    and case.prompt_variant == "baseline"
                }
                self.assertEqual({"default", "high"}, efforts)

    def test_cache_ab_has_isolated_two_request_arms(self):
        cases = [case for case in MODULE.matrix_cases("core-safe") if case.scenario == "cache"]
        self.assertEqual(6, len(cases))
        for mode in ("shared", "unique", "omitted"):
            arm = [case for case in cases if case.session_mode == mode]
            self.assertEqual(2, len(arm))
            requests = [MODULE.build_request(case, MODULE.DEFAULT_MODEL, "ab") for case in arm]
            self.assertEqual(requests[0]["messages"], requests[1]["messages"])
            if mode == "shared":
                self.assertEqual(requests[0]["session_id"], requests[1]["session_id"])
            elif mode == "unique":
                self.assertNotEqual(requests[0]["session_id"], requests[1]["session_id"])
            else:
                self.assertNotIn("session_id", requests[0])
                self.assertNotIn("session_id", requests[1])

        first_requests = {
            case.session_mode: MODULE.build_request(case, MODULE.DEFAULT_MODEL, "ab")
            for case in cases
            if case.case_id.endswith("_r1")
        }
        self.assertNotEqual(
            first_requests["shared"]["messages"],
            first_requests["unique"]["messages"],
        )
        self.assertNotEqual(
            first_requests["unique"]["messages"],
            first_requests["omitted"]["messages"],
        )

    def test_request_does_not_override_openrouter_provider_order(self):
        case = MODULE.MatrixCase("test", 12, 8192, "high")
        request = MODULE.build_request(case, MODULE.DEFAULT_MODEL, "safe-session")
        self.assertEqual({"effort": "high"}, request["reasoning"])
        self.assertNotIn("provider", request)
        self.assertEqual("safe-session-h12-code", request["session_id"])

    def test_xhigh_and_reasoning_max_are_explicit(self):
        xhigh = MODULE.MatrixCase("x", 12, 32768, "xhigh")
        capped = MODULE.MatrixCase("m", 12, 8192, "max", reasoning_max_tokens=4096)
        self.assertEqual({"effort": "xhigh"}, MODULE.reasoning_payload(xhigh))
        self.assertEqual(
            {"enabled": True, "max_tokens": 4096},
            MODULE.reasoning_payload(capped),
        )

    def test_cost_estimate_is_positive_and_conservative(self):
        case = MODULE.MatrixCase("test", 50, 8192, "high")
        request = MODULE.build_request(case, MODULE.DEFAULT_MODEL, "session")
        approx = MODULE.approximate_input_tokens(request)
        estimate = MODULE.worst_case_cost_usd(approx, case.max_tokens, 1.40, 4.40)
        self.assertGreater(approx, 0)
        self.assertGreater(estimate, 0)

    def test_stream_event_distinguishes_reasoning_from_visible_output(self):
        reasoning = {"choices": [{"delta": {"reasoning": "internal"}}]}
        content = {"choices": [{"delta": {"content": "answer"}}]}
        tool = {"choices": [{"delta": {"tool_calls": [{"index": 0}]}}]}
        self.assertEqual((True, False), MODULE.event_kinds(reasoning))
        self.assertEqual((False, True), MODULE.event_kinds(content))
        self.assertEqual((False, True), MODULE.event_kinds(tool))

    def test_observed_terminal_kind_accepts_protocol_results(self):
        state = MODULE.ResponseState(reasoning_chars=12)
        self.assertEqual("reasoning_only", MODULE.observed_terminal_kind(state))
        state.refusal = "cannot comply"
        self.assertEqual("refusal", MODULE.observed_terminal_kind(state))
        state.tool_call_indexes.add(0)
        self.assertEqual("tool_calls", MODULE.observed_terminal_kind(state))
        state.content = "final"
        self.assertEqual("content", MODULE.observed_terminal_kind(state))

    def test_sse_parser_and_success_require_done_and_consumable_terminal(self):
        self.assertEqual(("ignore", None), MODULE.parse_sse_line(b"event: ping\n"))
        self.assertEqual(("done", None), MODULE.parse_sse_line(b"data: [DONE]\n"))
        kind, payload = MODULE.parse_sse_line(b'data: {"choices":[]}\n')
        self.assertEqual("event", kind)
        self.assertEqual({"choices": []}, payload)

        result = {
            "status": 200,
            "error": "",
            "stream": True,
            "stream_complete": True,
            "terminal_valid": True,
        }
        self.assertTrue(MODULE.result_is_success(result))
        result["stream_complete"] = False
        self.assertFalse(MODULE.result_is_success(result))
        result["stream_complete"] = True
        result["terminal_valid"] = False
        self.assertFalse(MODULE.result_is_success(result))

    def test_execute_case_rejects_reasoning_only_stream_without_done(self):
        response = FakeHTTPResponse(
            [b'data: {"choices":[{"delta":{"reasoning":"think"}}]}\n']
        )
        with tempfile.TemporaryDirectory() as output_dir, mock.patch.object(
            MODULE.urllib.request,
            "urlopen",
            return_value=response,
        ):
            case = MODULE.MatrixCase("incomplete", 2, 8192, "high")
            result = MODULE.execute_case(
                case,
                "test",
                MODULE.build_request(case, MODULE.DEFAULT_MODEL, "test"),
                pathlib.Path(output_dir),
                MODULE.DEFAULT_URL,
                "test-key-never-sent",
                1,
                "",
                "test",
                0.01,
            )
        self.assertEqual("reasoning_only", result["observed_terminal_kind"])
        self.assertFalse(result["saw_done"])
        self.assertFalse(result["stream_complete"])
        self.assertFalse(result["terminal_valid"])
        self.assertIn("before [DONE]", result["error"])
        self.assertFalse(MODULE.result_is_success(result))

    def test_execute_case_accepts_content_stream_with_done(self):
        response = FakeHTTPResponse(
            [
                b'data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n',
                b"data: [DONE]\n",
            ]
        )
        with tempfile.TemporaryDirectory() as output_dir, mock.patch.object(
            MODULE.urllib.request,
            "urlopen",
            return_value=response,
        ):
            case = MODULE.MatrixCase("complete", 2, 8192, "high")
            result = MODULE.execute_case(
                case,
                "test",
                MODULE.build_request(case, MODULE.DEFAULT_MODEL, "test"),
                pathlib.Path(output_dir),
                MODULE.DEFAULT_URL,
                "test-key-never-sent",
                1,
                "",
                "test",
                0.01,
            )
        self.assertEqual("content", result["observed_terminal_kind"])
        self.assertTrue(result["saw_done"])
        self.assertTrue(result["stream_complete"])
        self.assertTrue(result["terminal_valid"])
        self.assertEqual("", result["error"])
        self.assertTrue(MODULE.result_is_success(result))


if __name__ == "__main__":
    unittest.main()
