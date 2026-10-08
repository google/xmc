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
import unittest
from unittest.mock import MagicMock

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.orchestrator import E2EOrchestrator
from test.e2e.lib.phases import Phase, PhaseError


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
        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()

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

    def test_run_tags_helm_failure_after_images_were_built(self):
        self._mock_components()
        self.orch.helm.deploy.side_effect = RuntimeError("rollout timed out")

        with self.assertRaises(PhaseError) as ctx:
            self.orch.run()

        self.assertIs(ctx.exception.phase, Phase.HELM_DEPLOY)
        self.assertTrue(self.orch.images_built)
        self.orch.tester.execute.assert_not_called()

    def test_run_tears_down_and_exits_with_signal_exit_code(self):
        for signum in (signal.SIGINT, signal.SIGTERM):
            self.addCleanup(signal.signal, signum, signal.getsignal(signum))
        self._mock_components()
        self.orch.tester.execute.side_effect = lambda: signal.raise_signal(signal.SIGTERM)

        with (
            self.assertLogs("xmc_e2e_runner", level="WARNING"),
            self.assertRaises(SystemExit) as ctx,
        ):
            self.orch.run()

        self.assertEqual(ctx.exception.code, 143)
        self.orch.kind.teardown.assert_called_once()
        self.orch.spanner.teardown.assert_called_once()


if __name__ == "__main__":
    unittest.main()
