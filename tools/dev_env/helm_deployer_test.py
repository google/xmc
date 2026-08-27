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

"""Unit tests for HelmDeployer with chart sanitizer and diagnostics."""

from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, call, patch

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
        self.deployer = HelmDeployer(
            config=self.config,
            repo_root=self.temp_path,
            temp_dir=self.temp_path,
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

    def test_prepare_local_chart_pruning_and_patching(self) -> None:
        helm_src = self.temp_path / "deploy" / "helm" / "templates"
        (helm_src / "ess").mkdir(parents=True, exist_ok=True)
        (helm_src / "certs").mkdir(parents=True, exist_ok=True)

        # Create prune targets and surviving targets
        (helm_src / "certs" / "dummy.yaml").write_text("dummy")
        (helm_src / "ess" / "gateway.yaml").write_text("gateway")
        (helm_src / "ess" / "deployment.yaml").write_text(
            "imagePullPolicy: Always\nother: Always\nimagePullPolicy: 'Always'"
        )

        prepared_dir = self.deployer.prepare_local_chart()
        self.assertTrue(prepared_dir.is_dir())
        templates_dir = prepared_dir / "templates"

        # Prune targets should be removed
        self.assertFalse((templates_dir / "certs").exists())
        self.assertFalse((templates_dir / "ess" / "gateway.yaml").exists())

        # Surviving template should have imagePullPolicy patched
        surviving = templates_dir / "ess" / "deployment.yaml"
        self.assertTrue(surviving.exists())
        content = surviving.read_text(encoding="utf-8")
        self.assertIn("imagePullPolicy: IfNotPresent", content)
        self.assertNotIn("imagePullPolicy: Always", content)

    def test_generate_values_file_with_auxiliary_services(self) -> None:
        cfg = LocalDevConfig(
            project_id="test-proj",
            enabled_services=[
                "experiment_state_server",
                "dashboard_service",
                "health_service",
                "components",
                "rod_data_provider",
                "ingestion_gateway",
            ],
        )
        val_path = self.deployer.generate_values_file(config=cfg)
        content = val_path.read_text(encoding="utf-8")
        self.assertIn("dashboardServiceDeployment", content)
        self.assertIn("dashboard_service_image:latest", content)
        self.assertIn("healthServiceDeployment", content)
        self.assertIn("health_service_image:latest", content)
        self.assertIn("componentsDeployment", content)
        self.assertIn("rodDeployment", content)
        self.assertIn("rod_data_provider_image:latest", content)
        self.assertIn("ingestionGatewayDeployment", content)
        self.assertIn("ingestion_gateway_image:latest", content)

    def test_prepare_local_chart_source_not_found(self) -> None:
        deployer = HelmDeployer(
            config=self.config,
            repo_root=self.temp_path / "empty_repo",
            temp_dir=self.temp_path,
        )
        with self.assertRaises(FileNotFoundError) as ctx:
            deployer.prepare_local_chart()
        self.assertIn("Source Helm chart directory not found", str(ctx.exception))

    def test_prepare_local_chart_read_only_template(self) -> None:
        helm_src = self.temp_path / "deploy" / "helm" / "templates" / "ess"
        helm_src.mkdir(parents=True, exist_ok=True)
        dep_file = helm_src / "deployment.yaml"
        dep_file.write_text("imagePullPolicy: Always\n", encoding="utf-8")
        dep_file.chmod(0o444)

        prepared_dir = self.deployer.prepare_local_chart()
        surviving = prepared_dir / "templates" / "ess" / "deployment.yaml"
        self.assertTrue(surviving.exists())
        content = surviving.read_text(encoding="utf-8")
        self.assertIn("imagePullPolicy: IfNotPresent", content)

    @patch("tools.dev_env.helm_deployer.shutil.which")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_capture_diagnostics_custom_namespace(
        self, mock_run: MagicMock, mock_which: MagicMock
    ) -> None:
        mock_which.return_value = "/usr/local/bin/kubectl"
        mock_run.return_value = subprocess.CompletedProcess([], 0, stdout="NAME STATUS", stderr="")

        diag_path = self.deployer.capture_diagnostics(namespace="custom-ns")
        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("=== PODS (namespace: custom-ns) ===", content)
        self.assertIn("=== PODS (namespace: dex) ===", content)

    @patch("tools.dev_env.helm_deployer.shutil.which")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_capture_diagnostics(self, mock_run: MagicMock, mock_which: MagicMock) -> None:
        mock_which.return_value = "/usr/local/bin/kubectl"

        def side_effect(cmd: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
            cmd_str = " ".join(cmd)
            if "get pods" in cmd_str and "-o jsonpath" in cmd_str:
                return subprocess.CompletedProcess(cmd, 0, stdout="pod-1 pod-2", stderr="")
            if "logs" in cmd_str:
                return subprocess.CompletedProcess(cmd, 0, stdout="Container log line", stderr="")
            if "get events" in cmd_str:
                return subprocess.CompletedProcess(
                    cmd, 0, stdout="Event: Normal Scheduled", stderr=""
                )
            return subprocess.CompletedProcess(cmd, 0, stdout="NAME STATUS", stderr="")

        mock_run.side_effect = side_effect
        diag_path = self.deployer.capture_diagnostics()

        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("=== PODS (namespace: xmc) ===", content)
        self.assertIn("=== POD DESCRIPTIONS (namespace: xmc) ===", content)
        self.assertIn("=== EVENTS (namespace: xmc) ===", content)
        self.assertIn("--- Logs for pod: pod-1 (ns: xmc) ---", content)
        self.assertIn("Container log line", content)

    @patch("tools.dev_env.helm_deployer.shutil.which")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_capture_diagnostics_no_kubectl(
        self, mock_run: MagicMock, mock_which: MagicMock
    ) -> None:
        mock_which.return_value = None
        diag_path = self.deployer.capture_diagnostics()

        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("kubectl CLI not found", content)
        mock_run.assert_not_called()

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
                "rollout",
                "status",
                "deployment/dex",
                "-n",
                "dex",
                "--timeout=120s",
            ]
        )
        for dep in ("actuator-deployment", "gateway-proxy", "xid-service-deployment"):
            mock_run.assert_any_call(
                [
                    "kubectl",
                    "--context",
                    "kind-test-kind-cluster",
                    "rollout",
                    "status",
                    f"deployment/{dep}",
                    "-n",
                    "xmc",
                    "--timeout=120s",
                ]
            )

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_skips_when_no_deployments(self, mock_run: MagicMock) -> None:
        def fake_run(cmd: list[str], *args: object, **kwargs: object) -> MagicMock:
            mock_res = MagicMock()
            if "deployment/dex" in cmd:
                mock_res.returncode = 1
                mock_res.stdout = b""
            else:
                mock_res.returncode = 0
                mock_res.stdout = b""
            return mock_res

        mock_run.side_effect = fake_run
        self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=120)

        for call_item in mock_run.call_args_list:
            cmd = call_item[0][0]
            self.assertNotIn("status", cmd)

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_discovery_failure_raises(self, mock_run: MagicMock) -> None:
        mock_res = MagicMock()
        mock_res.returncode = 1
        mock_res.stdout = b""
        mock_res.stderr = b"Connection refused"
        mock_run.return_value = mock_res

        with self.assertRaises(RuntimeError) as ctx:
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=120)
        self.assertIn("Failed to discover deployments", str(ctx.exception))

    def test_build_values_dict_direct(self) -> None:
        values = self.deployer._build_values_dict(self.config)
        self.assertEqual(values["projectId"], "test-proj")
        self.assertEqual(values["environment"], "local-dev")
        self.assertIn(
            "projects/test-proj/instances/test-instance/databases/test_ess_db",
            values["experimentStateServer"]["spannerDatabaseName"],
        )

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_discover_namespace_deployments_success(self, mock_run: MagicMock) -> None:
        mock_res = MagicMock()
        mock_res.returncode = 0
        mock_res.stdout = b"b-dep a-dep c-dep"
        mock_run.return_value = mock_res

        deps = self.deployer._discover_namespace_deployments("kind-test", "xmc")
        self.assertEqual(deps, ["a-dep", "b-dep", "c-dep"])

    @patch("tools.dev_env.helm_deployer.run_command")
    @patch.object(HelmDeployer, "capture_diagnostics")
    @patch("tools.dev_env.helm_deployer.run_command")
    def test_wait_for_rollouts_failure_captures_diagnostics(
        self, mock_run: MagicMock, mock_diag: MagicMock
    ) -> None:
        mock_run.side_effect = RuntimeError("Rollout timed out")
        with self.assertRaises(RuntimeError):
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=10)
        mock_diag.assert_called_once_with(namespace="xmc")

    @patch("tools.dev_env.helm_deployer.run_command")
    @patch.object(HelmDeployer, "capture_diagnostics")
    def test_wait_for_rollouts_failure_diagnostic_exception_suppressed(
        self, mock_diag: MagicMock, mock_run: MagicMock
    ) -> None:
        mock_run.side_effect = RuntimeError("Rollout timed out")
        mock_diag.side_effect = RuntimeError("Diagnostic disk full error")
        with self.assertRaises(RuntimeError) as ctx:
            self.deployer.wait_for_rollouts(namespace="xmc", timeout_seconds=10)
        self.assertEqual(str(ctx.exception), "Rollout timed out")
        mock_diag.assert_called_once_with(namespace="xmc")

    @patch("tools.dev_env.helm_deployer.run_command")
    @patch.object(HelmDeployer, "prepare_local_chart")
    def test_deploy_uses_prepared_chart(self, mock_prep: MagicMock, mock_run: MagicMock) -> None:
        mock_run.return_value.returncode = 0
        mock_run.return_value.stdout = b"gateway-proxy"
        mock_run.return_value.stderr = b""
        prepared_dir = self.temp_path / "prepared_helm"
        prepared_dir.mkdir(parents=True, exist_ok=True)
        mock_prep.return_value = prepared_dir
        self.deployer.deploy(timeout=60)

        mock_prep.assert_called_once()
        mock_run.assert_any_call(
            [
                "helm",
                "upgrade",
                "--install",
                "xmc",
                str(self.temp_path / "prepared_helm"),
                "--kube-context",
                "kind-test-kind-cluster",
                "--create-namespace",
                "-n",
                "xmc",
                "-f",
                str(self.deployer.values_file),
            ]
        )

        # Verify gateway-proxy restart
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

    @patch("tools.dev_env.helm_deployer.run_command")
    def test_deploy_with_values_local_yaml(self, mock_run: MagicMock) -> None:
        mock_run.return_value.returncode = 0
        mock_run.return_value.stdout = b"gateway-proxy"
        mock_run.return_value.stderr = b""
        chart_dir = self.temp_path / "deploy" / "helm"
        chart_dir.mkdir(parents=True, exist_ok=True)
        (chart_dir / "values-local.yaml").write_text("isDevEnvironment: true\n", encoding="utf-8")
        self.deployer.deploy(chart_path=chart_dir, timeout=60)

        mock_run.assert_any_call(
            [
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
        )

    def test_deploy_nonexistent_chart_dir_raises(self) -> None:
        nonexistent = self.temp_path / "nonexistent" / "helm"
        with self.assertRaises(FileNotFoundError) as ctx:
            self.deployer.deploy(chart_path=nonexistent)
        self.assertIn("Helm chart directory not found", str(ctx.exception))

    def test_deploy_nonexistent_custom_values_file_raises(self) -> None:
        chart_dir = self.temp_path / "deploy" / "helm"
        chart_dir.mkdir(parents=True, exist_ok=True)
        nonexistent_values = self.temp_path / "nonexistent" / "values.yaml"
        with self.assertRaises(FileNotFoundError) as ctx:
            self.deployer.deploy(chart_path=chart_dir, values_path=nonexistent_values)
        self.assertIn("Custom values file not found", str(ctx.exception))

    @patch("getpass.getuser", return_value="testuser")
    def test_default_temp_dir_user_specific(self, mock_getuser: MagicMock) -> None:
        deployer = HelmDeployer(config=self.config, repo_root=self.temp_path)
        expected_path = self.temp_path / ".xmc_dev" / f"testuser_{self.config.cluster_name}"
        self.assertEqual(deployer.temp_dir, expected_path)

    @patch("getpass.getuser", side_effect=KeyError("No username found"))
    @patch.dict("os.environ", {"USER": "fallback_user"})
    def test_default_temp_dir_fallback_user_env(self, mock_getuser: MagicMock) -> None:
        deployer = HelmDeployer(config=self.config, repo_root=self.temp_path)
        expected_path = self.temp_path / ".xmc_dev" / f"fallback_user_{self.config.cluster_name}"
        self.assertEqual(deployer.temp_dir, expected_path)

    @patch("getpass.getuser", side_effect=KeyError("No username found"))
    @patch("os.getuid", return_value=1001, create=True)
    def test_default_temp_dir_fallback_getuid(
        self, mock_getuid: MagicMock, mock_getuser: MagicMock
    ) -> None:
        with patch.dict("os.environ", {}, clear=True):
            deployer = HelmDeployer(config=self.config, repo_root=self.temp_path)
            expected_path = self.temp_path / ".xmc_dev" / f"1001_{self.config.cluster_name}"
            self.assertEqual(deployer.temp_dir, expected_path)

    @patch("getpass.getuser", side_effect=KeyError("No username found"))
    def test_default_temp_dir_fallback_unknown(self, mock_getuser: MagicMock) -> None:
        with patch.dict("os.environ", {}, clear=True):
            with patch.object(os, "getuid", side_effect=OSError("no uid"), create=True):
                deployer = HelmDeployer(config=self.config, repo_root=self.temp_path)
                expected_path = self.temp_path / ".xmc_dev" / f"unknown_{self.config.cluster_name}"
                self.assertEqual(deployer.temp_dir, expected_path)


if __name__ == "__main__":
    unittest.main()
