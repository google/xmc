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

"""Unit tests for E2EOrchestrator."""

import signal
import tempfile
import threading
import time
import unittest
from unittest.mock import MagicMock, patch

from test.e2e.lib import cancellation
from test.e2e.lib.cancellation import HarnessStoppedError
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.orchestrator import E2EOrchestrator
from test.e2e.lib.phases import Phase, PhaseError

# Upper bound for a run whose halted sibling task would otherwise block for a minute.
_HALT_BOUND_SECONDS = 10


class TestE2EOrchestrator(unittest.TestCase):
    """Tests for E2EOrchestrator."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            temp_dir=self.tmp_dir.name,
        )
        self.orch = E2EOrchestrator(self.cfg)
        self.controller = cancellation.StopController()
        patcher = patch.object(cancellation, "CONTROLLER", self.controller)
        patcher.start()
        self.addCleanup(patcher.stop)

    def tearDown(self):
        self.tmp_dir.cleanup()

    def _mock_components(self) -> None:
        for name in ("spanner", "kind", "builder", "helm", "tester"):
            setattr(self.orch, name, MagicMock())
        self.orch.builder.build_all.return_value = ["img:latest"]

    def test_managed_lifecycle_guarantees_teardown_on_exit(self):
        self.orch.spanner = MagicMock()
        self.orch.kind = MagicMock()

        with self.orch.managed_lifecycle():
            # Teardown not called yet inside block
            self.orch.spanner.teardown.assert_not_called()
            self.orch.kind.teardown.assert_not_called()

        # Teardown called on exit
        self.orch.spanner.teardown.assert_called_once()
        self.orch.kind.teardown.assert_called_once()

    def test_run_executes_all_pipeline_stages(self):
        self.orch.spanner = MagicMock()
        self.orch.kind = MagicMock()
        self.orch.builder = MagicMock()
        self.orch.helm = MagicMock()
        self.orch.tester = MagicMock()

        self.orch.builder.build_all.return_value = ["img:latest"]

        self.orch.run()

        self.orch.spanner.setup.assert_called_once()
        self.orch.kind.setup.assert_called_once()
        self.orch.builder.build_all.assert_called_once()
        self.orch.kind.load_images.assert_called_once_with(["img:latest"])
        self.orch.helm.deploy.assert_called_once()
        self.orch.tester.execute.assert_called_once()
        self.orch.helm.capture_diagnostics.assert_not_called()
        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()

    def test_run_captures_cluster_diagnostics_before_teardown_when_tests_fail(self) -> None:
        self._mock_components()
        events: list[str] = []
        self.orch.helm.capture_diagnostics.side_effect = lambda: events.append("diagnostics")
        self.orch.kind.teardown.side_effect = lambda: events.append("teardown")
        self.orch.tester.execute.side_effect = RuntimeError("work unit FAILED")

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()

        self.assertIs(ctx.exception.phase, Phase.TESTS)
        self.assertIn("work unit FAILED", str(ctx.exception.__cause__))
        self.assertEqual(events, ["diagnostics", "teardown"])

    def test_managed_lifecycle_preserves_cluster_when_keep_cluster_set(self):
        self.cfg.keep_cluster = True
        self.orch.spanner = MagicMock()
        self.orch.kind = MagicMock()
        with self.orch.managed_lifecycle():
            pass

        self.orch.kind.teardown.assert_not_called()
        self.orch.spanner.teardown.assert_not_called()

    def test_run_fails_fast_when_phase1_task_fails(self):
        self.orch.spanner = MagicMock()
        self.orch.kind = MagicMock()
        self.orch.builder = MagicMock()
        self.orch.kind.setup.side_effect = RuntimeError("KinD setup failed")

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()
        self.assertIs(ctx.exception.phase, Phase.KIND_CREATE)
        self.assertIn("KinD setup failed", str(ctx.exception.__cause__))
        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()

    def test_run_fails_fast_when_phase2_task_fails(self):
        self.orch.spanner = MagicMock()
        self.orch.kind = MagicMock()
        self.orch.builder = MagicMock()
        self.orch.builder.build_all.return_value = ["img:latest"]
        self.orch.spanner.setup.side_effect = RuntimeError("Spanner setup failed")

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()
        self.assertIs(ctx.exception.phase, Phase.SPANNER_SETUP)
        self.assertIn("Spanner setup failed", str(ctx.exception.__cause__))
        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()

    def test_run_halts_running_sibling_task_when_phase_task_fails(self):
        self._mock_components()
        build_started = threading.Event()

        def _build_until_halted():
            build_started.set()
            cancellation.CONTROLLER.wait(60)

        def _fail_kind_setup():
            build_started.wait(_HALT_BOUND_SECONDS)
            raise RuntimeError("KinD setup failed")

        self.orch.builder.build_all.side_effect = _build_until_halted
        self.orch.kind.setup.side_effect = _fail_kind_setup
        start = time.monotonic()

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()

        self.assertLess(time.monotonic() - start, _HALT_BOUND_SECONDS)
        self.assertIs(ctx.exception.phase, Phase.KIND_CREATE)
        self.assertFalse(self.orch.images_built)
        self.orch.kind.teardown.assert_called_once()

    def test_run_tags_helm_failure_after_images_were_built(self):
        self._mock_components()
        self.orch.helm.deploy.side_effect = RuntimeError("rollout timed out")

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()

        self.assertIs(ctx.exception.phase, Phase.HELM_DEPLOY)
        self.assertTrue(self.orch.images_built)
        self.orch.tester.execute.assert_not_called()

    def test_run_tears_down_and_propagates_harness_stop(self):
        self._mock_components()
        self.orch.tester.execute.side_effect = HarnessStoppedError()

        with self.assertRaises(HarnessStoppedError):
            self.orch.run()

        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()

    def test_managed_lifecycle_reports_stop_signal_before_teardown(self):
        self._mock_components()
        self.controller.request_stop(signal.SIGINT)

        with self.assertLogs("xmc_e2e_runner", level="WARNING") as logs:
            with self.orch.managed_lifecycle():
                pass

        self.assertEqual(logs.output, ["WARNING:xmc_e2e_runner:Stopped by signal 2; tearing down."])
        self.orch.kind.teardown.assert_called_once()


if __name__ == "__main__":
    unittest.main()
