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

"""Unit tests for the E2E runner entrypoint."""

import unittest
from unittest.mock import MagicMock, patch

from test.e2e import runner
from test.e2e.lib.outcome import AttemptResult, HarnessResult, Outcome
from test.e2e.lib.phases import Phase


class TestMain(unittest.TestCase):
    """Tests for the runner exit codes."""

    def setUp(self):
        self.cfg = MagicMock()
        patcher = patch.object(runner, "parse_args", return_value=self.cfg)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_returns_exit_code_of_final_outcome(self):
        cases = [
            (AttemptResult(Outcome.PASSED), 0),
            (AttemptResult.for_failed_phase(Phase.HELM_DEPLOY, images_built=True), 1),
            (AttemptResult.for_failed_phase(Phase.KIND_CREATE, images_built=False), 3),
        ]
        for attempt, expected_exit_code in cases:
            with self.subTest(outcome=attempt.outcome.name):
                result = HarnessResult(attempts=(attempt,))
                with patch.object(runner, "run_with_infra_retry", return_value=result) as retry:
                    exit_code = runner.main()

                self.assertEqual(exit_code, expected_exit_code)
                retry.assert_called_once_with(self.cfg, runner.run_attempt)

    def test_returns_harness_error_for_unexpected_exception(self):
        with (
            patch.object(runner, "run_with_infra_retry", side_effect=FileNotFoundError("kind")),
            self.assertLogs("xmc_e2e_runner", level="ERROR"),
        ):
            self.assertEqual(runner.main(), 4)

    def test_propagates_system_exit(self):
        with (
            patch.object(runner, "run_with_infra_retry", side_effect=SystemExit(143)),
            self.assertRaises(SystemExit) as ctx,
        ):
            runner.main()

        self.assertEqual(ctx.exception.code, 143)


if __name__ == "__main__":
    unittest.main()
