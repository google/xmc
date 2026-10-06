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

"""Unit tests for the run-level infra retry policy."""

import contextlib
import dataclasses
import io
import unittest
from unittest.mock import MagicMock, patch

from test.e2e.lib import infra_retry
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.outcome import AttemptResult, Outcome
from test.e2e.lib.phases import Phase, PhaseError

_PASSED = AttemptResult(outcome=Outcome.PASSED, images_built=True)
_PRODUCT_FAILURE = AttemptResult.for_failed_phase(Phase.TESTS, images_built=True)
_INFRA_FAILURE = AttemptResult.for_failed_phase(Phase.SPANNER_SETUP, images_built=True)


def _make_config(keep_cluster: bool = False) -> E2EConfig:
    return E2EConfig(
        project_id="test-proj",
        spanner_instance="test-inst",
        cluster_name="test-cluster",
        kind_bin="kind",
        workspace_root="/workspace",
        screenshots_dir="/screenshots",
        keep_cluster=keep_cluster,
        ess_db="e2e-ess-1000-abcdef",
        xid_db="e2e-xid-1000-abcdef",
        temp_dir="/tmp/e2e_test",
    )


class TestShouldRetry(unittest.TestCase):
    """Tests for the pure retry decision."""

    def test_retries_only_early_infra_failures_with_attempts_left(self):
        cases = [
            ("early infra failure", _make_config(), _INFRA_FAILURE, 1, 100, True),
            ("infra failure at window end", _make_config(), _INFRA_FAILURE, 1, 480, True),
            ("infra failure after window", _make_config(), _INFRA_FAILURE, 1, 481, False),
            ("no attempts left", _make_config(), _INFRA_FAILURE, 2, 100, False),
            ("keep cluster", _make_config(keep_cluster=True), _INFRA_FAILURE, 1, 100, False),
            ("product failure", _make_config(), _PRODUCT_FAILURE, 1, 100, False),
            ("pass", _make_config(), _PASSED, 1, 100, False),
        ]
        for name, cfg, result, attempt, elapsed, expected in cases:
            with self.subTest(name):
                self.assertIs(infra_retry.should_retry(cfg, result, attempt, elapsed), expected)


class TestAttemptConfig(unittest.TestCase):
    """Tests for the configuration of each attempt."""

    def test_first_attempt_keeps_resources_and_writes_to_its_own_directory(self):
        cfg = _make_config()

        first = infra_retry.attempt_config(cfg, 1, reuse_images=False)

        self.assertEqual(first, dataclasses.replace(cfg, screenshots_dir="/screenshots/attempt-1"))

    def test_retry_gets_fresh_resources_and_skips_build_of_built_images(self):
        cfg = _make_config()
        for reuse_images in (True, False):
            with self.subTest(reuse_images=reuse_images):
                retry = infra_retry.attempt_config(cfg, 2, reuse_images=reuse_images)

                self.assertEqual(retry.screenshots_dir, "/screenshots/attempt-2")
                self.assertIs(retry.skip_build, reuse_images)
                self.assertEqual(retry.cluster_name, cfg.cluster_name)
                for field in ("ess_db", "xid_db", "temp_dir"):
                    self.assertNotEqual(getattr(retry, field), getattr(cfg, field))


class TestRunWithInfraRetry(unittest.TestCase):
    """Tests for the harness retry loop."""

    def _run(self, cfg: E2EConfig, results: list[AttemptResult]):
        attempt_runner = MagicMock(side_effect=results)
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            harness_result = infra_retry.run_with_infra_retry(cfg, attempt_runner)
        return harness_result, attempt_runner, stdout.getvalue()

    def test_retries_early_infra_failure_once_with_built_images(self):
        cfg = _make_config()

        result, attempt_runner, stdout = self._run(cfg, [_INFRA_FAILURE, _PASSED])

        self.assertEqual(result.attempts, (_INFRA_FAILURE, _PASSED))
        first_cfg, retry_cfg = (call.args[0] for call in attempt_runner.call_args_list)
        self.assertEqual(first_cfg, infra_retry.attempt_config(cfg, 1, reuse_images=False))
        self.assertEqual(retry_cfg.screenshots_dir, "/screenshots/attempt-2")
        self.assertTrue(retry_cfg.skip_build)
        self.assertEqual(
            stdout,
            "::warning::E2E infra failure in phase spanner-setup on attempt 1/2; retrying\n",
        )

    def test_reports_infra_failure_when_retry_also_fails(self):
        result, attempt_runner, stdout = self._run(_make_config(), [_INFRA_FAILURE] * 2)

        self.assertIs(result.outcome, Outcome.INFRA_FAILURE)
        self.assertEqual(attempt_runner.call_count, 2)
        self.assertEqual(stdout.count("::warning::"), 1)

    def test_does_not_retry_infra_failure_that_ended_after_window(self):
        with patch.object(infra_retry.time, "monotonic", side_effect=[0.0, 481.0]):
            result, _, stdout = self._run(_make_config(), [_INFRA_FAILURE])

        self.assertEqual(result.attempts, (_INFRA_FAILURE,))
        self.assertEqual(stdout, "")


class TestRunAttempt(unittest.TestCase):
    """Tests for single-attempt classification."""

    def setUp(self):
        patcher = patch.object(infra_retry, "E2EOrchestrator")
        self.orchestrator = patcher.start().return_value
        self.addCleanup(patcher.stop)
        self.orchestrator.images_built = True
        self.cfg = _make_config()

    def test_returns_passed_when_run_succeeds(self):
        self.assertEqual(infra_retry.run_attempt(self.cfg), _PASSED)

    def test_classifies_phase_error_and_logs_it(self):
        self.orchestrator.run.side_effect = PhaseError(Phase.SPANNER_SETUP)

        with self.assertLogs("xmc_e2e_runner", level="ERROR") as logs:
            result = infra_retry.run_attempt(self.cfg)

        self.assertEqual(result, _INFRA_FAILURE)
        self.assertIn("spanner-setup", logs.output[0])


if __name__ == "__main__":
    unittest.main()
