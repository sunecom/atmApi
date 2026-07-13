#!/usr/bin/env python3
import importlib.util
import pathlib
import sys
import unittest


SCRIPT = pathlib.Path(__file__).with_name("glm52_v021_phase0a.py")
SPEC = importlib.util.spec_from_file_location("glm52_v021_phase0a", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class Phase0ATest(unittest.TestCase):
    def test_profiles_have_unique_case_ids(self):
        for profile, expected_count in (("smoke", 3), ("core", 21), ("extended", 27)):
            cases = MODULE.matrix_cases(profile)
            self.assertEqual(expected_count, len(cases))
            self.assertEqual(len(cases), len({case.case_id for case in cases}))

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


if __name__ == "__main__":
    unittest.main()
