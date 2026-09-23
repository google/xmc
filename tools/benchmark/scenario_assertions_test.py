# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for the scenario_assertions tool."""

import argparse
import json
import tempfile
import unittest
from pathlib import Path

from tools.benchmark.scenario_assertions import (
    NEGATIVE,
    ORIGIN,
    TRANSFER,
    ScenarioOutcome,
    Verdict,
    check_scenario_assertions,
    derive_verdict,
    evaluate_assertion,
    evaluate_scenario,
    evaluate_spec,
    load_spec,
    render_report,
)
from tools.common.tool_utils import HandledError


def _outcome(scenario: str, *, passed: bool) -> ScenarioOutcome:
    """Builds a scenario outcome with no assertion detail, for verdict-matrix tests."""
    return ScenarioOutcome(scenario=scenario, passed=passed, assertions=())


class EvaluateAssertionTest(unittest.TestCase):
    """Covers each predicate kind against a real temporary workspace."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.workspace = Path(self._tmp.name)
        (self.workspace / "present.py").write_text("import os\nBANNED = 1\n", encoding="utf-8")

    def test_file_exists_passes_when_present(self):
        outcome = evaluate_assertion(self.workspace, {"kind": "file_exists", "path": "present.py"})
        self.assertTrue(outcome.passed)
        self.assertEqual(outcome.description, "file_exists present.py")

    def test_file_exists_fails_when_missing(self):
        outcome = evaluate_assertion(self.workspace, {"kind": "file_exists", "path": "nope.py"})
        self.assertFalse(outcome.passed)
        self.assertEqual(outcome.detail, "absent")

    def test_file_absent_inverts_file_exists(self):
        outcome = evaluate_assertion(self.workspace, {"kind": "file_absent", "path": "nope.py"})
        self.assertTrue(outcome.passed)

    def test_file_contains_matches_regex(self):
        outcome = evaluate_assertion(
            self.workspace,
            {"kind": "file_contains", "path": "present.py", "pattern": r"^BANNED"},
        )
        self.assertTrue(outcome.passed)

    def test_file_contains_fails_on_missing_file(self):
        outcome = evaluate_assertion(
            self.workspace,
            {"kind": "file_contains", "path": "nope.py", "pattern": "anything"},
        )
        self.assertFalse(outcome.passed)
        self.assertIn("file absent", outcome.detail)

    def test_file_omits_passes_when_pattern_missing(self):
        outcome = evaluate_assertion(
            self.workspace,
            {"kind": "file_omits", "path": "present.py", "pattern": "ALLOWED"},
        )
        self.assertTrue(outcome.passed)

    def test_unknown_kind_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(self.workspace, {"kind": "file_vibes", "path": "present.py"})

    def test_missing_path_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(self.workspace, {"kind": "file_exists"})

    def test_missing_pattern_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(self.workspace, {"kind": "file_contains", "path": "present.py"})

    def test_non_string_pattern_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(
                self.workspace,
                {"kind": "file_contains", "path": "present.py", "pattern": 123},
            )

    def test_invalid_regex_pattern_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(
                self.workspace,
                {"kind": "file_contains", "path": "present.py", "pattern": "[unclosed"},
            )

    def test_absolute_path_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(
                self.workspace,
                {"kind": "file_exists", "path": "/etc/passwd"},
            )

    def test_parent_traversal_path_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_assertion(
                self.workspace,
                {"kind": "file_exists", "path": "../outside.py"},
            )


class EvaluateScenarioTest(unittest.TestCase):
    """Covers scenario aggregation and its guard rails."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.workspace = Path(self._tmp.name)
        (self.workspace / "a.py").write_text("ok\n", encoding="utf-8")

    def test_scenario_passes_only_when_all_assertions_pass(self):
        spec = {
            "workspace": str(self.workspace),
            "assertions": [
                {"kind": "file_exists", "path": "a.py"},
                {"kind": "file_exists", "path": "b.py"},
            ],
        }
        outcome = evaluate_scenario(ORIGIN, spec)
        self.assertFalse(outcome.passed)
        self.assertEqual(len(outcome.assertions), 2)

    def test_scenario_without_assertions_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_scenario(ORIGIN, {"workspace": str(self.workspace), "assertions": []})

    def test_scenario_with_bad_workspace_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_scenario(
                ORIGIN,
                {
                    "workspace": str(self.workspace / "missing"),
                    "assertions": [{"kind": "file_exists", "path": "a.py"}],
                },
            )
        with self.assertRaises(HandledError):
            evaluate_scenario(
                ORIGIN,
                {
                    "workspace": None,
                    "assertions": [{"kind": "file_exists", "path": "a.py"}],
                },
            )

        with self.assertRaises(HandledError):
            evaluate_scenario(
                ORIGIN,
                {
                    "assertions": [{"kind": "file_exists", "path": "a.py"}],
                },
            )

    def test_non_mapping_scenario_or_assertion_is_rejected(self):
        with self.assertRaises(HandledError):
            evaluate_scenario(ORIGIN, "not-a-dict")  # type: ignore[arg-type]
        with self.assertRaises(HandledError):
            evaluate_scenario(
                ORIGIN,
                {"workspace": str(self.workspace), "assertions": "not-a-list"},
            )
        with self.assertRaises(HandledError):
            evaluate_scenario(
                ORIGIN,
                {"workspace": str(self.workspace), "assertions": ["not-an-object"]},
            )


class DeriveVerdictTest(unittest.TestCase):
    """Covers every cell of the verdict matrix."""

    def test_failed_origin_rejects_regardless_of_the_rest(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=False),
            TRANSFER: _outcome(TRANSFER, passed=True),
            NEGATIVE: _outcome(NEGATIVE, passed=True),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.REJECTED)

    def test_all_three_pass_ships(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            TRANSFER: _outcome(TRANSFER, passed=True),
            NEGATIVE: _outcome(NEGATIVE, passed=True),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.SHIP)

    def test_failed_negative_is_over_generalized(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            TRANSFER: _outcome(TRANSFER, passed=True),
            NEGATIVE: _outcome(NEGATIVE, passed=False),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.OVER_GENERALIZED)

    def test_failed_transfer_is_under_generalized(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            TRANSFER: _outcome(TRANSFER, passed=False),
            NEGATIVE: _outcome(NEGATIVE, passed=True),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.UNDER_GENERALIZED)

    def test_both_generalization_scenarios_failing_is_a_wrong_predicate(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            TRANSFER: _outcome(TRANSFER, passed=False),
            NEGATIVE: _outcome(NEGATIVE, passed=False),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.WRONG_PREDICATE)

    def test_origin_only_ships_with_an_explicit_caveat(self):
        outcomes = {ORIGIN: _outcome(ORIGIN, passed=True)}
        self.assertEqual(derive_verdict(outcomes), Verdict.SHIP_ORIGIN_ONLY)

    def test_failed_transfer_with_missing_negative_is_under_generalized(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            TRANSFER: _outcome(TRANSFER, passed=False),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.UNDER_GENERALIZED)

    def test_failed_negative_with_missing_transfer_is_over_generalized(self):
        outcomes = {
            ORIGIN: _outcome(ORIGIN, passed=True),
            NEGATIVE: _outcome(NEGATIVE, passed=False),
        }
        self.assertEqual(derive_verdict(outcomes), Verdict.OVER_GENERALIZED)

    def test_missing_origin_is_rejected(self):
        with self.assertRaises(HandledError):
            derive_verdict({TRANSFER: _outcome(TRANSFER, passed=True)})


class RenderReportTest(unittest.TestCase):
    """Covers markdown rendering, including the not-run marker."""

    def test_unrun_scenarios_render_as_dashes(self):
        outcomes = {ORIGIN: _outcome(ORIGIN, passed=True)}
        report = render_report(outcomes, Verdict.SHIP_ORIGIN_ONLY)
        self.assertIn("| ✅ | — | — |", report)
        self.assertIn("GENERALIZATION UNTESTED", report)

    def test_assertion_detail_rows_are_rendered_and_pipes_escaped(self):
        with tempfile.TemporaryDirectory() as tmp:
            workspace = Path(tmp)
            (workspace / "a.py").write_text("ok\n", encoding="utf-8")
            outcome = evaluate_scenario(
                ORIGIN,
                {
                    "workspace": str(workspace),
                    "assertions": [
                        {"kind": "file_contains", "path": "a.py", "pattern": r"ok|yes"},
                    ],
                },
            )
        report = render_report({ORIGIN: outcome}, Verdict.SHIP_ORIGIN_ONLY)
        self.assertIn(r"| origin | `file_contains a.py` | ✅ | /ok\|yes/ matched |", report)


class LoadAndEvaluateSpecTest(unittest.TestCase):
    """Covers spec loading and the end-to-end evaluation path."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.workspace = Path(self._tmp.name)
        (self.workspace / "a.py").write_text("ok\n", encoding="utf-8")

    def _write_spec(self, payload: object) -> Path:
        spec_path = self.workspace / "spec.json"
        spec_path.write_text(json.dumps(payload), encoding="utf-8")
        return spec_path

    def test_round_trip_produces_a_ship_verdict(self):
        scenario = {
            "workspace": str(self.workspace),
            "assertions": [{"kind": "file_exists", "path": "a.py"}],
        }
        spec_path = self._write_spec({ORIGIN: scenario, TRANSFER: scenario, NEGATIVE: scenario})
        _, verdict = evaluate_spec(load_spec(spec_path))
        self.assertEqual(verdict, Verdict.SHIP)

    def test_check_scenario_assertions_writes_output_report(self):
        scenario = {
            "workspace": str(self.workspace),
            "assertions": [{"kind": "file_exists", "path": "a.py"}],
        }
        spec_path = self._write_spec({ORIGIN: scenario})
        out_path = self.workspace / "report.md"
        res = check_scenario_assertions(
            argparse.Namespace(spec=str(spec_path), output=str(out_path))
        )
        self.assertEqual(res["verdict"], str(Verdict.SHIP_ORIGIN_ONLY))
        self.assertTrue(out_path.is_file())
        self.assertIn("Pre-Registered Scenario Verdict", out_path.read_text(encoding="utf-8"))

    def test_unknown_scenario_key_is_rejected(self):
        spec_path = self._write_spec({"orgin": {}})
        with self.assertRaises(HandledError):
            load_spec(spec_path)

    def test_non_object_spec_is_rejected(self):
        spec_path = self._write_spec([1, 2, 3])
        with self.assertRaises(HandledError):
            load_spec(spec_path)

    def test_missing_spec_file_is_rejected(self):
        with self.assertRaises(HandledError):
            load_spec(self.workspace / "absent.json")

    def test_malformed_json_is_rejected(self):
        spec_path = self.workspace / "spec.json"
        spec_path.write_text("{not json", encoding="utf-8")
        with self.assertRaises(HandledError):
            load_spec(spec_path)


if __name__ == "__main__":
    unittest.main()
