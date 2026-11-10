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

"""Unit tests for HelmDeployer orchestration."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.helm_deployer import (
    HelmDeployer,
    _dump_yaml,
    service_to_deployment_key,
    service_to_image_key,
)


class TestHelmDeployer(unittest.TestCase):
    """Test suite for HelmDeployer."""

    def test_service_to_deployment_key(self) -> None:
        self.assertEqual(
            service_to_deployment_key("dashboard_service"), "dashboardServiceDeployment"
        )
        self.assertEqual(service_to_deployment_key("health_service"), "healthServiceDeployment")
        self.assertEqual(
            service_to_deployment_key("ingestion_gateway"), "ingestionGatewayDeployment"
        )
        self.assertEqual(service_to_deployment_key("components"), "componentsDeployment")
        self.assertEqual(service_to_deployment_key("xmc_bridge"), "componentsDeployment")
        self.assertEqual(service_to_deployment_key("rod_data_provider"), "rodDeployment")
        self.assertEqual(service_to_deployment_key("rod"), "rodDeployment")
        self.assertEqual(service_to_deployment_key("experiment_state_server"), "essDeployment")
        self.assertEqual(service_to_deployment_key("ess"), "essDeployment")

    def test_service_to_image_key(self) -> None:
        self.assertEqual(
            service_to_image_key("dashboard_service"), "dashboard_service_custom_image"
        )
        self.assertEqual(service_to_image_key("experiment_state_server"), "ess_custom_image")
        self.assertEqual(service_to_image_key("rod_data_provider"), "rod_custom_image")
        self.assertEqual(service_to_image_key("components"), "xmc_bridge_custom_image")
        self.assertEqual(service_to_image_key("xmc_bridge"), "xmc_bridge_custom_image")
        self.assertEqual(service_to_image_key("custom_service"), "custom_service_custom_image")

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.tmp_dir.name)
        self.config = LocalDevConfig(
            project_id="test-proj",
            environment="local-dev",
            spanner_instance="test-instance",
            ess_database="test_ess_db",
            xid_database="test_xid_db",
            cluster_name="test-kind-cluster",
        )
        self.mock_sanitizer = MagicMock()
        self.mock_diagnostics = MagicMock()
        self.deployer = HelmDeployer(
            config=self.config,
            repo_root=self.temp_path,
            temp_dir=self.temp_path,
            sanitizer=self.mock_sanitizer,
            diagnostics=self.mock_diagnostics,
        )

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    def test_dump_yaml_json_fallback(self) -> None:
        data = {"key": "val", "number": 42, "items": ["a", "b"]}
        with patch("tools.dev_env.helm_deployer._HAS_YAML", False):
            result = _dump_yaml(data)
            self.assertIn('"key": "val"', result)
            self.assertIn('"number": 42', result)
            self.assertIn('"items": [', result)

    def test_kube_context(self) -> None:
        self.assertEqual(self.deployer.kube_context, "kind-test-kind-cluster")

    def test_generate_values_file_default(self) -> None:
        val_path = self.deployer.generate_values_file()
        self.assertTrue(val_path.is_file())
        content = val_path.read_text(encoding="utf-8")
        self.assertIn("test-proj", content)
        self.assertIn("local-dev", content)
        self.assertIn("test-instance", content)
        self.assertIn("test_ess_db", content)
        self.assertIn("test_xid_db", content)
        self.assertIn("isDevEnvironment: true", content)
        self.assertIn("/etc/gcp-secrets/key.json", content)
        self.assertIn(
            "gcpServiceAccount: xmc-v2-local-dev-xmc-wl-sa@test-proj.iam.gserviceaccount.com",
            content,
        )

    def test_prepare_local_chart_delegates_to_sanitizer(self) -> None:
        self.mock_sanitizer.prepare_sanitized_chart.return_value = self.temp_path / "sanitized"
        res = self.deployer.prepare_local_chart()
        self.assertEqual(res, self.temp_path / "sanitized")
        self.mock_sanitizer.prepare_sanitized_chart.assert_called_once()

    def test_capture_diagnostics_delegates_to_diagnostics(self) -> None:
        self.mock_diagnostics.capture_diagnostics.return_value = self.temp_path / "diag.log"
        res = self.deployer.capture_diagnostics(namespace="test-ns")
        self.assertEqual(res, self.temp_path / "diag.log")
        self.mock_diagnostics.capture_diagnostics.assert_called_once_with(namespace="test-ns")

    def test_generate_values_file_with_auxiliary_services(self) -> None:
        cfg = LocalDevConfig(
            project_id="test-proj",
            enabled_services=[
                "experiment_state_server",
                "dashboard_service",
                "health_service",
                "components",
                "rod_data_provider",
            ],
        )
        val_path = self.deployer.generate_values_file(config=cfg)
        content = val_path.read_text(encoding="utf-8")

        self.assertIn("essDeployment", content)
        self.assertIn("dashboardServiceDeployment", content)
        self.assertIn("healthServiceDeployment", content)
        self.assertIn("rodDeployment", content)
        self.assertNotIn("componentsDeployment", content)
        self.assertNotIn("ingestionGatewayDeployment", content)
        self.assertIn("ess_custom_image", content)
        self.assertIn("dashboard_service_custom_image", content)
        self.assertIn("xmc_bridge_custom_image", content)
        # ingestion-gateway is a Component actuated by xmc-bridge; KinD neither builds its image
        # nor sets its image value.
        self.assertNotIn("ingestion_gateway_custom_image", content)

    def test_generate_values_file_with_remote_k8s(self) -> None:
        cfg = LocalDevConfig(
            project_id="test-proj",
            remote_k8s_context="gke_proj_us-central1_cluster-1",
        )
        val_path = self.deployer.generate_values_file(config=cfg)
        content = val_path.read_text(encoding="utf-8")

        self.assertIn("remoteK8s:", content)
        self.assertIn("enabled: true", content)
        self.assertIn("secretName: remote-k8s-config", content)
        self.assertIn("clusterName: cluster-1", content)
        self.assertIn("clusterLocation: us-central1", content)
        self.assertIn(
            "gcpServiceAccount: xmc-v2-dev-xmc-wl-sa@test-proj.iam.gserviceaccount.com",
            content,
        )

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts(self, mock_run: MagicMock) -> None:
        def fake_run(cmd: list[str], *args: object, **kwargs: object) -> MagicMock:
            mock_res = MagicMock()
            if "get" in cmd and "deployments" in cmd:
                mock_res.returncode = 0
                mock_res.stdout = b"gateway-proxy actuator-deployment xid-service-deployment"
            elif "get" in cmd and "deployment/dex" in cmd:
                mock_res.returncode = 0
                mock_res.stdout = b"deployment.apps/dex"
            else:
                mock_res.returncode = 0
                mock_res.stdout = b""
            return mock_res

        mock_run.side_effect = fake_run
        self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=120)

        mock_run.assert_any_call(
            [
                "kubectl",
                "--context",
                "kind-test-kind-cluster",
                "get",
                "deployment/dex",
                "-n",
                "dex",
            ],
            check=False,
            capture_output=True,
            log_cmd=False,
        )
        mock_run.assert_any_call(
            [
                "kubectl",
                "--context",
                "kind-test-kind-cluster",
                "rollout",
                "status",
                "deployment/dex",
                "-n",
                "dex",
                "--timeout=120s",
            ]
        )

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_discovery_failure_raises(self, mock_run: MagicMock) -> None:
        def fake_run(cmd: list[str], *args: object, **kwargs: object) -> MagicMock:
            mock_res = MagicMock()
            if "get" in cmd and "deployments" in cmd:
                mock_res.returncode = 1
                mock_res.stderr = b"API server unavailable"
            else:
                mock_res.returncode = 1
            return mock_res

        mock_run.side_effect = fake_run
        with self.assertRaises(RuntimeError) as ctx:
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=10)
        self.assertIn("Failed to discover deployments in namespace xmc", str(ctx.exception))
        self.mock_diagnostics.capture_diagnostics.assert_called_once_with(namespace="xmc")

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_failure_triggers_diagnostics_and_raises(
        self, mock_run: MagicMock
    ) -> None:
        def fake_run(cmd: list[str], *args: object, **kwargs: object) -> MagicMock:
            mock_res = MagicMock()
            if "get" in cmd and "deployments" in cmd:
                mock_res.returncode = 0
                mock_res.stdout = b"failed-deployment"
            elif "rollout" in cmd and "status" in cmd:
                raise RuntimeError("Deployment failed-deployment rollout timed out")
            else:
                mock_res.returncode = 0
                mock_res.stdout = b""
            return mock_res

        mock_run.side_effect = fake_run

        with self.assertRaises(RuntimeError):
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=10)

        self.mock_diagnostics.capture_diagnostics.assert_called_once_with(namespace="xmc")

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_diagnostics_exception_does_not_mask_rollout_failure(
        self, mock_run: MagicMock
    ) -> None:
        def fake_run(cmd: list[str], *args: object, **kwargs: object) -> MagicMock:
            mock_res = MagicMock()
            if "get" in cmd and "deployments" in cmd:
                mock_res.returncode = 0
                mock_res.stdout = b"broken-deployment"
            elif "rollout" in cmd and "status" in cmd:
                raise RuntimeError("Original rollout timeout error")
            else:
                mock_res.returncode = 0
                mock_res.stdout = b""
            return mock_res

        mock_run.side_effect = fake_run
        self.mock_diagnostics.capture_diagnostics.side_effect = Exception(
            "Diagnostic disk full error"
        )

        with self.assertRaises(RuntimeError) as ctx:
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=10)

        self.assertIn("Original rollout timeout error", str(ctx.exception))
        self.mock_diagnostics.capture_diagnostics.assert_called_once_with(namespace="xmc")

    @patch.object(HelmDeployer, "wait_for_rollouts")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_deploy_executes_helm_and_restarts_gateway(
        self, mock_run: MagicMock, mock_wait: MagicMock
    ) -> None:
        chart_dir = self.temp_path / "chart"
        chart_dir.mkdir(parents=True, exist_ok=True)
        (chart_dir / "values-local.yaml").write_text("isDev: true\n")

        self.deployer.deploy(chart_path=chart_dir, namespace="xmc", timeout=60)

        helm_cmd = [
            "helm",
            "upgrade",
            "--install",
            "xmc",
            str(chart_dir),
            "--kube-context",
            "kind-test-kind-cluster",
            "--create-namespace",
            "-f",
            str(chart_dir / "values-local.yaml"),
            "-n",
            "xmc",
            "-f",
            str(self.deployer.values_file),
        ]
        mock_run.assert_any_call(helm_cmd)
        mock_run.assert_any_call(
            [
                "kubectl",
                "--context",
                "kind-test-kind-cluster",
                "rollout",
                "restart",
                "deployment/gateway-proxy",
                "-n",
                "xmc",
            ],
            check=False,
        )
        mock_wait.assert_called_once_with(namespace="xmc", timeout_seconds=60)

    @patch.object(HelmDeployer, "wait_for_rollouts")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_deploy_with_explicit_values_path(
        self, mock_run: MagicMock, mock_wait: MagicMock
    ) -> None:
        chart_dir = self.temp_path / "chart"
        chart_dir.mkdir(parents=True, exist_ok=True)
        custom_val = self.temp_path / "my-values.yaml"
        custom_val.write_text("custom: true\n")

        self.deployer.deploy(
            chart_path=chart_dir,
            values_path=custom_val,
            namespace="xmc",
            timeout=60,
        )

        mock_wait.assert_called_once_with(namespace="xmc", timeout_seconds=60)

    def test_deploy_nonexistent_chart_path_raises(self) -> None:
        with self.assertRaises(FileNotFoundError) as ctx:
            self.deployer.deploy(chart_path=Path("/nonexistent/chart/dir"))
        self.assertIn("Helm chart directory not found", str(ctx.exception))

    def test_deploy_nonexistent_values_path_raises(self) -> None:
        chart_dir = self.temp_path / "chart"
        chart_dir.mkdir(parents=True, exist_ok=True)
        with self.assertRaises(FileNotFoundError) as ctx:
            self.deployer.deploy(
                chart_path=chart_dir,
                values_path=Path("/nonexistent/values.yaml"),
            )
        self.assertIn("Custom values file not found", str(ctx.exception))

    def test_deploy_string_paths_handled(self) -> None:
        with self.assertRaises(FileNotFoundError):
            self.deployer.deploy(chart_path="/nonexistent/chart/dir")

        chart_dir = self.temp_path / "chart"
        chart_dir.mkdir(parents=True, exist_ok=True)
        with self.assertRaises(FileNotFoundError):
            self.deployer.deploy(
                chart_path=str(chart_dir),
                values_path="/nonexistent/values.yaml",
            )
