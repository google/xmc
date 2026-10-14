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

"""Unit tests for HelmDeployer."""

import os
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.helm_deployer import HelmDeployer


class TestHelmDeployer(unittest.TestCase):
    """Tests for HelmDeployer."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.screenshots_dir = os.path.join(self.tmp_dir.name, "screenshots")
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir=self.screenshots_dir,
            ess_db="e2e-ess-1234-abc",
            xid_db="e2e-xid-1234-abc",
            temp_dir=self.tmp_dir.name,
        )
        self.deployer = HelmDeployer(self.cfg)

    def tearDown(self):
        self.tmp_dir.cleanup()

    def test_patch_image_pull_policy(self):
        # Create a mock template in helm_dir
        os.makedirs(os.path.join(self.deployer.helm_dir, "templates"), exist_ok=True)
        sample_yaml = os.path.join(self.deployer.helm_dir, "templates", "deploy.yaml")
        with open(sample_yaml, "w", encoding="utf-8") as f:
            f.write(
                "spec:\n"
                "  containers:\n"
                "    - imagePullPolicy: Always\n"
                '    - imagePullPolicy: "Always"\n'
            )

        self.deployer._patch_image_pull_policy()

        with open(sample_yaml, encoding="utf-8") as f:
            content = f.read()
        self.assertEqual(
            content,
            "spec:\n"
            "  containers:\n"
            "    - imagePullPolicy: IfNotPresent\n"
            "    - imagePullPolicy: IfNotPresent\n",
        )

    def test_generate_values_file(self) -> None:
        self.deployer._generate_values_file()

        self.assertTrue(os.path.isfile(self.deployer.values_file))
        with open(self.deployer.values_file, encoding="utf-8") as f:
            values = f.read()

        self.assertIn('projectId: "test-proj"', values)
        self.assertIn("e2e-ess-1234-abc", values)
        self.assertIn("e2e-xid-1234-abc", values)
        self.assertIn('jwksUrl: "http://dex.dex.svc.cluster.local:5556/dex/keys"', values)
        self.assertIn('issuerUrl: "http://localhost:5556/dex"', values)
        self.assertIn('allowedEmails: "admin@xmc.local,viewer@xmc.local"', values)
        self.assertIn('username: "admin@xmc.local"', values)
        self.assertIn('password: "password123"', values)
        self.assertIn('xmc_bridge_custom_image: "xmc_bridge_image:latest"', values)
        self.assertIn("componentsDeployment:\n  enabled: true", values)

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_wait_for_rollouts_success(self, mock_run) -> None:
        self.deployer._wait_for_rollouts()

        context = f"kind-{self.cfg.cluster_name}"
        mock_run.assert_any_call(
            [
                "kubectl",
                "--context",
                context,
                "rollout",
                "status",
                "deployment/dex",
                "-n",
                "dex",
                "--timeout=180s",
            ]
        )
        for deploy_name in (
            "xid-service-deployment",
            "experiment-state-server-deployment",
            "actuator-deployment",
            "reconciler-deployment",
            "xmc-bridge-deployment",
            "ui-deployment",
            "gateway-proxy",
        ):
            mock_run.assert_any_call(
                [
                    "kubectl",
                    "--context",
                    context,
                    "rollout",
                    "status",
                    f"deployment/{deploy_name}",
                    "-n",
                    "xmc",
                    "--timeout=180s",
                ]
            )

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_wait_for_rollouts_failure_captures_diagnostics(self, mock_run):
        def _side_effect(cmd, *args, **kwargs):
            if "rollout" in cmd:
                raise RuntimeError("Rollout timed out")
            return unittest.mock.MagicMock(stdout="mock-pod-1", stderr="")

        mock_run.side_effect = _side_effect

        with self.assertRaises(RuntimeError):
            self.deployer._wait_for_rollouts()

        diag_file = os.path.join(self.cfg.screenshots_dir, "k8s_diagnostics.log")
        self.assertTrue(os.path.isfile(diag_file))
        with open(diag_file, encoding="utf-8") as f:
            diagnostics = f.read()
        for ns in ("xmc", "dex", "jobset-system", "kueue-system", "cpu-queue"):
            self.assertIn(f"=== PODS (ns: {ns}) ===", diagnostics)
            self.assertIn(f"=== EVENTS (ns: {ns}) ===", diagnostics)
        self.assertIn("--- Logs for pod: mock-pod-1 ---", diagnostics)

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_capture_diagnostics_logs_and_swallows_kubectl_failures(
        self, mock_run: unittest.mock.MagicMock
    ) -> None:
        """A missing or hung kubectl is logged instead of escaping the failure handler."""
        for error in (
            FileNotFoundError("kubectl"),
            subprocess.TimeoutExpired(cmd="kubectl", timeout=900),
        ):
            with self.subTest(error=type(error).__name__):
                mock_run.side_effect = error

                with self.assertLogs(level="WARNING") as logs:
                    self.deployer.capture_diagnostics()

                self.assertIn(
                    "WARNING:xmc_e2e_runner:Failed to capture cluster diagnostics", logs.output[-1]
                )

    def test_prepare_helm_chart_prunes_unsupported_resources_and_keeps_kueue_dev_and_wpc(self):
        src_helm = os.path.join(self.cfg.temp_dir, "workspace_helm")
        os.makedirs(os.path.join(src_helm, "templates", "certs"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "kueue-dev"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "wpc"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "ess"), exist_ok=True)
        with open(
            os.path.join(src_helm, "templates", "kueue-dev", "cpu-queue.yaml"),
            "w",
            encoding="utf-8",
        ) as f:
            f.write("kind: ClusterQueue\n")
        with open(
            os.path.join(src_helm, "templates", "wpc", "exp_medium_wu_medium.yaml"),
            "w",
            encoding="utf-8",
        ) as f:
            f.write("kind: WorkloadPriorityClass\n")
        with open(
            os.path.join(src_helm, "templates", "ess", "gateway.yaml"), "w", encoding="utf-8"
        ) as f:
            f.write("kind: Gateway\n")
        with open(
            os.path.join(src_helm, "templates", "ess", "deployment.yaml"), "w", encoding="utf-8"
        ) as f:
            f.write("kind: Deployment\n")

        self.deployer.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root=self.cfg.temp_dir,
            screenshots_dir=self.cfg.screenshots_dir,
            temp_dir=self.cfg.temp_dir,
        )
        # Point deploy/helm inside workspace_root
        os.makedirs(os.path.join(self.cfg.temp_dir, "deploy"), exist_ok=True)
        os.rename(src_helm, os.path.join(self.cfg.temp_dir, "deploy", "helm"))

        self.deployer._prepare_helm_chart()

        self.assertTrue(
            os.path.isfile(
                os.path.join(self.deployer.helm_dir, "templates", "ess", "deployment.yaml")
            )
        )
        self.assertTrue(
            os.path.isfile(
                os.path.join(self.deployer.helm_dir, "templates", "kueue-dev", "cpu-queue.yaml")
            )
        )
        self.assertTrue(
            os.path.isfile(
                os.path.join(
                    self.deployer.helm_dir, "templates", "wpc", "exp_medium_wu_medium.yaml"
                )
            )
        )
        self.assertFalse(os.path.exists(os.path.join(self.deployer.helm_dir, "templates", "certs")))
        self.assertFalse(
            os.path.exists(os.path.join(self.deployer.helm_dir, "templates", "ess", "gateway.yaml"))
        )

    @patch("test.e2e.lib.helm_deployer.ControllerInstaller")
    def test_install_controllers_installs_all_releases(
        self, mock_installer_cls: unittest.mock.MagicMock
    ) -> None:
        self.deployer._install_controllers()

        mock_installer_cls.assert_called_once_with(self.cfg)
        mock_installer_cls.return_value.install_all.assert_called_once_with()

    @patch.object(HelmDeployer, "capture_diagnostics")
    @patch("test.e2e.lib.helm_deployer.ControllerInstaller")
    def test_install_controllers_failure_captures_diagnostics(
        self, mock_installer_cls: unittest.mock.MagicMock, mock_capture: unittest.mock.MagicMock
    ) -> None:
        mock_installer_cls.return_value.install_all.side_effect = RuntimeError("webhook down")

        with self.assertRaisesRegex(RuntimeError, "webhook down"):
            self.deployer._install_controllers()

        mock_capture.assert_called_once_with()

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    @patch("test.e2e.lib.helm_deployer.ControllerInstaller")
    def test_install_controllers_failure_is_not_masked_by_failing_diagnostics(
        self, mock_installer_cls: unittest.mock.MagicMock, _mock_run: unittest.mock.MagicMock
    ) -> None:
        """A diagnostics failure is logged while the original install error propagates."""
        mock_installer_cls.return_value.install_all.side_effect = RuntimeError("webhook down")
        # A regular file in place of the diagnostics directory makes writing the log fail.
        with open(self.screenshots_dir, "w", encoding="utf-8"):
            pass

        with (
            self.assertLogs(level="WARNING") as logs,
            self.assertRaisesRegex(RuntimeError, "webhook down"),
        ):
            self.deployer._install_controllers()

        self.assertIn(
            "WARNING:xmc_e2e_runner:Failed to capture cluster diagnostics", logs.output[-1]
        )

    @patch.object(HelmDeployer, "_prepare_helm_chart")
    @patch.object(HelmDeployer, "_patch_image_pull_policy")
    @patch.object(HelmDeployer, "_generate_values_file")
    @patch.object(HelmDeployer, "_install_controllers")
    @patch.object(HelmDeployer, "_wait_for_rollouts")
    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_deploy_orchestrates_release(
        self, mock_run, mock_wait, mock_install_controllers, mock_gen_vals, mock_patch, mock_prep
    ):
        self.deployer.deploy()

        mock_prep.assert_called_once()
        mock_patch.assert_called_once()
        mock_gen_vals.assert_called_once()
        mock_install_controllers.assert_called_once()
        mock_wait.assert_called_once()

        mock_run.assert_any_call(
            [
                "helm",
                "upgrade",
                "--install",
                "xmc",
                self.deployer.helm_dir,
                "--kube-context",
                "kind-test-cluster",
                "-n",
                "xmc",
                "-f",
                self.deployer.values_file,
            ]
        )


if __name__ == "__main__":
    unittest.main()
