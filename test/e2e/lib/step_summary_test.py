# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for the E2E GitHub Actions job summary."""

import os
import tempfile
import unittest
from unittest.mock import mock_open, patch

from test.e2e.lib.outcome import AttemptResult, HarnessResult, Outcome
from test.e2e.lib.phases import Phase
from test.e2e.lib.step_summary import append_step_summary, render_step_summary

_PASSED = AttemptResult(Outcome.PASSED, images_built=True)
_SPANNER_FAILURE = AttemptResult.for_failed_phase(Phase.SPANNER_SETUP, images_built=True)
_TESTS_FAILURE = AttemptResult.for_failed_phase(Phase.TESTS, images_built=True)
_DEADLINE_EXPIRED = AttemptResult(Outcome.INFRA_FAILURE, images_built=True)
_PASS_AFTER_RETRY = HarnessResult(attempts=(_SPANNER_FAILURE, _PASSED), duration_seconds=412.4)


class TestRenderStepSummary(unittest.TestCase):
    """Tests for summary rendering."""

    def test_renders_outcome_attempts_failures_and_duration(self):
        cases = [
            (_PASS_AFTER_RETRY, "| passed | 2 | spanner-setup (attempt 1) | 412 s |"),
            (
                HarnessResult(attempts=(_TESTS_FAILURE,), duration_seconds=200.0),
                "| product failure | 1 | tests (attempt 1) | 200 s |",
            ),
            (
                HarnessResult(
                    attempts=(_SPANNER_FAILURE, _DEADLINE_EXPIRED), duration_seconds=1020.0
                ),
                "| infra failure | 2 | spanner-setup (attempt 1), harness deadline (attempt 2) "
                "| 1020 s |",
            ),
            (
                HarnessResult(attempts=(_PASSED,), duration_seconds=1.0),
                "| passed | 1 | none | 1 s |",
            ),
        ]
        for result, expected_row in cases:
            with self.subTest(expected_row=expected_row):
                summary = render_step_summary(result)

                self.assertTrue(summary.startswith("### XMC Headless E2E Summary\n"))
                self.assertIn(expected_row, summary.splitlines())


class TestAppendStepSummary(unittest.TestCase):
    """Tests for writing the summary to GITHUB_STEP_SUMMARY."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp_dir.cleanup)
        self.summary_path = os.path.join(self.tmp_dir.name, "summary.md")

    def test_appends_to_existing_summary_file(self):
        with open(self.summary_path, "w", encoding="utf-8") as summary_file:
            summary_file.write("previous step\n")

        with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": self.summary_path}):
            append_step_summary(_PASS_AFTER_RETRY)

        with open(self.summary_path, encoding="utf-8") as summary_file:
            content = summary_file.read()
        self.assertEqual(content, "previous step\n" + render_step_summary(_PASS_AFTER_RETRY))

    def test_does_nothing_when_variable_unset(self):
        with patch.dict(os.environ, clear=True), patch("builtins.open") as open_mock:
            append_step_summary(_PASS_AFTER_RETRY)

        open_mock.assert_not_called()

    def test_logs_warning_instead_of_raising_on_write_error(self):
        missing_path = os.path.join(self.tmp_dir.name, "missing-dir", "summary.md")

        with (
            patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": missing_path}),
            self.assertLogs("xmc_e2e_runner", level="WARNING") as logs,
        ):
            append_step_summary(_PASS_AFTER_RETRY)

        self.assertIn(missing_path, logs.output[0])

    def test_logs_warning_instead_of_raising_on_unexpected_error(self):
        broken_file = mock_open()
        broken_file.return_value.write.side_effect = ValueError("I/O operation on closed file.")

        with (
            patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": self.summary_path}),
            patch("builtins.open", broken_file),
            self.assertLogs("xmc_e2e_runner", level="WARNING") as logs,
        ):
            append_step_summary(_PASS_AFTER_RETRY)

        self.assertIn("I/O operation on closed file.", logs.output[0])


if __name__ == "__main__":
    unittest.main()
