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

"""Unit tests for tools.dev_env.upgrade_dev_env."""

import io
import os
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from dataclasses import replace
from pathlib import Path
from unittest import mock

from tools.dev_env import upgrade_dev_env


class TestParseArgs(unittest.TestCase):
    """Tests for command-line argument parsing and mutual exclusion."""

    def test_defaults(self):
        args = upgrade_dev_env.parse_args([])
        self.assertEqual(args.config, "deploy/ctk_v2/xmanager-cloud-deployment.yaml")
        self.assertIsNone(args.start_from)
        self.assertIsNone(args.only)
        self.assertFalse(args.run_migrations)

    def test_custom_flags(self):
        args = upgrade_dev_env.parse_args(
            ["-c", "custom.yaml", "-s", "images", "--run-migrations", "--enable-components"]
        )
        self.assertEqual(args.config, "custom.yaml")
        self.assertEqual(args.start_from, "images")
        self.assertTrue(args.run_migrations)
        self.assertTrue(args.enable_components)

    def test_no_enable_components_rejected(self):
        err_stream = io.StringIO()
        with mock.patch("sys.stderr", err_stream), self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.parse_args(["--no-enable-components"])
        self.assertEqual(cm.exception.code, 1)
        self.assertIn(
            "--no-enable-components / enable_components=false is no longer supported",
            err_stream.getvalue(),
        )

    def test_mutually_exclusive_start_from_and_only(self):
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.parse_args(["-s", "check", "--only", "migrate"])
        self.assertEqual(cm.exception.code, 1)


class TestLoadConfig(unittest.TestCase):
    """Tests for configuration loading and precedence hierarchy."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.config_path = Path(self.tmp_dir.name) / "test_config.yaml"
        self.config_content = """vars:
  project_id: yaml-project
  deployment_name: yaml-deploy
  region: us-east1
  environment: staging
  namespace: custom-ns
  domain_name: example.com
  image_registry: us-docker.pkg.dev/yaml-project/xmc
  image_tag: latest
"""
        self.config_path.write_text(self.config_content, encoding="utf-8")

    def tearDown(self):
        self.tmp_dir.cleanup()

    @mock.patch.dict(os.environ, {}, clear=True)
    def test_yaml_values_loaded(self):
        config = upgrade_dev_env.load_config(str(self.config_path))
        self.assertEqual(config.project_id, "yaml-project")
        self.assertEqual(config.deployment_name, "yaml-deploy")
        self.assertEqual(config.region, "us-east1")
        self.assertEqual(config.environment, "staging")
        self.assertEqual(config.namespace, "custom-ns")
        self.assertEqual(config.gke_cluster, "xmc-yaml-deploy-staging")

    @mock.patch.dict(
        os.environ,
        {
            "PROJECT_ID": "env-project",
            "XMC_DEPLOYMENT_NAME": "env-deploy",
            "GCP_REGION": "us-west1",
        },
        clear=True,
    )
    def test_environment_precedence(self):
        config = upgrade_dev_env.load_config(str(self.config_path))
        self.assertEqual(config.project_id, "env-project")
        self.assertEqual(config.deployment_name, "env-deploy")
        self.assertEqual(config.region, "us-west1")

    @mock.patch.dict(os.environ, {}, clear=True)
    def test_missing_required_project_id_raises(self):
        bad_config = Path(self.tmp_dir.name) / "bad.yaml"
        bad_config.write_text("vars:\n  deployment_name: foo\n", encoding="utf-8")
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.load_config(str(bad_config))
        self.assertEqual(cm.exception.code, 1)


class TestMigrationHelpers(unittest.TestCase):
    """Tests for SQL version parsing, status detection, and exit code 10."""

    def test_extract_migration_version(self):
        files = [
            Path("0000_init.up.sql"),
            Path("001_baseline.up.sql"),
            Path("005_feature.up.sql"),
            Path("002_fix.up.sql"),
        ]
        max_ver = upgrade_dev_env.extract_migration_version(files)
        self.assertEqual(max_ver, 5)

    def test_extract_migration_version_empty(self):
        self.assertEqual(upgrade_dev_env.extract_migration_version([]), 0)

    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_get_migration_status_dirty(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="Version: 1\nDirty: true\n", stderr=""
        )
        config = upgrade_dev_env.UpgradeConfig(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="e",
            namespace="n",
            config_path="cfg",
            image_registry="us-docker.pkg.dev/p/xmc",
            image_tag="latest",
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.get_migration_status(config, "ess", "inst", "db")
        self.assertEqual(cm.exception.code, 1)

    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_get_migration_status_nonzero_exit(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=1, stdout="", stderr="rpc error: code = Unavailable"
        )
        config = upgrade_dev_env.UpgradeConfig(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="e",
            namespace="n",
            config_path="cfg",
            image_registry="us-docker.pkg.dev/p/xmc",
            image_tag="latest",
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.get_migration_status(config, "ess", "inst", "db")
        self.assertEqual(cm.exception.code, 1)

    @mock.patch("tools.dev_env.upgrade_dev_env.get_migration_status")
    def test_step_migrate_exits_code_10_when_pending(self, mock_status):
        mock_status.side_effect = [(1, 2), (1, 1)]
        config = upgrade_dev_env.UpgradeConfig(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="e",
            namespace="n",
            config_path="cfg",
            image_registry="us-docker.pkg.dev/p/xmc",
            image_tag="latest",
            run_migrations=False,
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_migrate(config)
        self.assertEqual(cm.exception.code, 10)

    @mock.patch("tools.dev_env.upgrade_dev_env.run_migration")
    @mock.patch("tools.dev_env.upgrade_dev_env.get_migration_status")
    def test_step_migrate_runs_when_flag_enabled(self, mock_status, mock_run_mig):
        mock_status.side_effect = [(1, 2), (1, 3)]
        config = upgrade_dev_env.UpgradeConfig(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="dev",
            namespace="n",
            config_path="cfg",
            image_registry="us-docker.pkg.dev/p/xmc",
            image_tag="latest",
            run_migrations=True,
        )
        upgrade_dev_env.step_migrate(config)
        self.assertEqual(mock_run_mig.call_count, 2)

    @mock.patch("tools.dev_env.upgrade_dev_env.run_migration")
    @mock.patch("tools.dev_env.upgrade_dev_env.get_migration_status")
    def test_step_migrate_skips_when_postgres_backend(self, mock_status, mock_run_mig):
        config = upgrade_dev_env.UpgradeConfig(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="dev",
            namespace="n",
            config_path="cfg",
            image_registry="us-docker.pkg.dev/p/xmc",
            image_tag="latest",
            database_backend="postgres",
            run_migrations=True,
        )
        self.assertEqual(upgrade_dev_env.check_pending_migrations(config), [])
        upgrade_dev_env.step_migrate(config)
        mock_status.assert_not_called()
        mock_run_mig.assert_not_called()


class TestDeploymentResolution(unittest.TestCase):
    """Tests Kubernetes deployment name fallback resolution."""

    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_resolve_exact_match(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertEqual(res, "actuator")

    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_resolve_fallback(self, mock_run):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        ]
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertEqual(res, "actuator-deployment")

    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_resolve_none_found(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=1, stdout="", stderr=""
        )
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertIsNone(res)


class TestStepSubprocesses(unittest.TestCase):
    """Tests hermetic subprocess calls per step."""

    def setUp(self):
        self.config = upgrade_dev_env.UpgradeConfig(
            project_id="proj-123",
            deployment_name="deploy-456",
            region="us-central1",
            environment="dev",
            namespace="xmc",
            config_path="deploy/ctk_v2/xmanager-cloud-deployment.yaml",
            image_registry="us-docker.pkg.dev/proj-123/xmc",
            image_tag="latest",
        )

    @mock.patch("tools.dev_env.upgrade_dev_env.verify_helm_min_version", return_value="v3.15.0")
    @mock.patch("shutil.which", return_value="/usr/bin/tool")
    @mock.patch.object(Path, "is_file", return_value=True)
    @mock.patch("os.access", return_value=True)
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    @mock.patch("tools.dev_env.upgrade_dev_env.configure_docker_auth")
    def test_step_check(
        self, mock_docker_auth, mock_run, _mock_access, _mock_file, _mock_which, _mock_helm
    ):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_check(self.config)
        calls = mock_run.call_args_list
        self.assertIn(
            mock.call(
                [
                    "gcloud",
                    "config",
                    "set",
                    "project",
                    "proj-123",
                    "--quiet",
                ]
            ),
            calls,
        )
        mock_docker_auth.assert_called_once_with(self.config)

    @mock.patch("tools.common.tool_utils.run_command")
    def test_step_images_default(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_images(self.config)
        self.assertEqual(mock_run.call_count, 12)

    @mock.patch.object(Path, "is_dir", return_value=True)
    @mock.patch("tools.dev_env.upgrade_dev_env.deploy_component_profile")
    @mock.patch("tools.dev_env.upgrade_dev_env.compile_blueprint")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_app(self, mock_run, mock_compile, mock_profile, _mock_dir):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_app(self.config)
        mock_compile.assert_called_once_with(
            "deploy/ctk_v2/xmanager-cloud-deployment.yaml", "deploy-456"
        )
        mock_profile.assert_called_once_with(self.config)
        # Verify export-outputs, import-inputs, and deploy calls are present
        calls = [call_args[0][0] for call_args in mock_run.call_args_list]
        self.assertIn(
            ["./cluster-toolkit/gcluster", "export-outputs", "ctk-out/deploy-456/primary"],
            calls,
        )
        self.assertIn(
            ["./cluster-toolkit/gcluster", "import-inputs", "ctk-out/deploy-456/compute"],
            calls,
        )
        self.assertIn(
            ["./cluster-toolkit/gcluster", "export-outputs", "ctk-out/deploy-456/compute"],
            calls,
        )
        self.assertIn(
            [
                "./cluster-toolkit/gcluster",
                "import-inputs",
                "ctk-out/deploy-456/kubernetes_prereqs",
            ],
            calls,
        )
        self.assertIn(
            [
                "./cluster-toolkit/gcluster",
                "--download-dependencies",
                "deploy",
                "ctk-out/deploy-456",
                "--only",
                "xmc_app",
                "--auto-approve",
            ],
            calls,
        )

    @mock.patch("tools.dev_env.upgrade_dev_env.resolve_deployment_name")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_restart(self, mock_run, mock_resolve):
        mock_resolve.return_value = "actuator"
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_restart(self.config)
        self.assertEqual(mock_run.call_count, 12)

    def _restart_cmd(self, deployment: str) -> list[str]:
        return [
            "kubectl",
            "rollout",
            "restart",
            f"deployment/{deployment}",
            "-n",
            self.config.namespace,
        ]

    @mock.patch("tools.dev_env.upgrade_dev_env.resolve_deployment_name")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_restart_triggers_bridge_and_gateway(self, mock_run, mock_resolve):
        # xmc-study-controller is the Deployment's real name; the others use the -deployment suffix.
        mock_resolve.side_effect = lambda service, _ns: (
            service if service == "xmc-study-controller" else f"{service}-deployment"
        )
        upgrade_dev_env.step_restart(self.config)
        cmds = [call.args[0] for call in mock_run.call_args_list]
        self.assertIn(self._restart_cmd("xmc-bridge-deployment"), cmds)
        self.assertIn(self._restart_cmd("ingestion-gateway-deployment"), cmds)
        self.assertIn(self._restart_cmd("xmc-study-controller"), cmds)

    @mock.patch("tools.dev_env.upgrade_dev_env.resolve_deployment_name")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_restart_skips_bridge_and_gateway_when_absent(self, mock_run, mock_resolve):
        absent = {"xmc-bridge", "ingestion-gateway", "xmc-study-controller"}
        mock_resolve.side_effect = lambda service, _ns: (
            None if service in absent else f"{service}-deployment"
        )
        upgrade_dev_env.step_restart(self.config)
        cmds = [call.args[0] for call in mock_run.call_args_list]
        self.assertEqual(mock_run.call_count, 9)
        self.assertNotIn(self._restart_cmd("xmc-bridge-deployment"), cmds)
        self.assertNotIn(self._restart_cmd("ingestion-gateway-deployment"), cmds)
        self.assertNotIn(self._restart_cmd("xmc-study-controller"), cmds)

    @mock.patch("tools.dev_env.upgrade_dev_env.resolve_deployment_name", return_value=None)
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_restart_fails_when_zero_deployments_resolved(self, mock_run, mock_resolve):
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_restart(self.config)
        self.assertEqual(cm.exception.code, 1)
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.upgrade_dev_env.wait_for_deployments_ready")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_step_verify(self, mock_run, mock_wait_rollout):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_verify(self.config)
        mock_wait_rollout.assert_called_once_with(self.config)
        mock_run.assert_called_once()
        _cmd, kwargs = mock_run.call_args
        self.assertEqual(
            kwargs["env"],
            {
                "ENVIRONMENT": "dev",
                "REGION": "us-central1",
                "PROJECT_ID": "proj-123",
                "LOG_MONITOR_DEPLOYMENT_ENABLED": "true",
            },
        )

    @mock.patch("tools.dev_env.upgrade_dev_env.wait_for_deployments_ready")
    @mock.patch(
        "tools.dev_env.upgrade_dev_env.run_command",
        side_effect=subprocess.CalledProcessError(1, ["python3", "verify_xmc.py"]),
    )
    def test_step_verify_propagates_failure(self, mock_run, mock_wait_rollout):
        with self.assertRaises(subprocess.CalledProcessError):
            upgrade_dev_env.step_verify(self.config)
        mock_wait_rollout.assert_called_once_with(self.config)

    @mock.patch("tools.dev_env.upgrade_dev_env.resolve_deployment_name")
    @mock.patch("tools.dev_env.upgrade_dev_env.run_command")
    def test_wait_for_deployments_ready(self, mock_run, mock_resolve):
        absent = {"xmc-bridge", "ingestion-gateway", "xmc-study-controller"}
        mock_resolve.side_effect = lambda service, _ns: (
            None if service in absent else f"{service}-deployment"
        )
        upgrade_dev_env.wait_for_deployments_ready(self.config)
        self.assertEqual(mock_run.call_count, 9)
        for call_args in mock_run.call_args_list:
            cmd = call_args[0][0]
            self.assertEqual(cmd[:3], ["kubectl", "rollout", "status"])
            self.assertIn("-n", cmd)
            self.assertIn("xmc", cmd)
            self.assertIn("--timeout=180s", cmd)

    def test_parse_args_profile_values(self):
        parsed = upgrade_dev_env.parse_args(["--profile-values", "/tmp/pv.yaml"])
        self.assertEqual(parsed.profile_values, "/tmp/pv.yaml")

    @mock.patch("tools.dev_env.upgrade_dev_env.log_error")
    @mock.patch(
        "tools.dev_env.upgrade_dev_env.verify_helm_min_version",
        side_effect=RuntimeError("Helm version must be >= 3.14.0"),
    )
    @mock.patch("shutil.which", return_value="/usr/bin/tool")
    def test_step_check_fails_old_helm(self, _mock_which, _mock_helm, mock_log_error):
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_check(self.config)
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once_with(
            "Failed Helm version check: Helm version must be >= 3.14.0"
        )

    @mock.patch("tools.dev_env.upgrade_dev_env.log_error")
    @mock.patch(
        "tools.dev_env.upgrade_dev_env.verify_helm_min_version",
        side_effect=PermissionError("helm not executable"),
    )
    @mock.patch("shutil.which", return_value="/usr/bin/tool")
    def test_step_check_fails_helm_oserror(self, _mock_which, _mock_helm, mock_log_error):
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_check(self.config)
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once_with("Failed Helm version check: helm not executable")

    @mock.patch("tools.dev_env.upgrade_dev_env.log_error")
    @mock.patch("tools.dev_env.upgrade_dev_env.Path.is_file", return_value=False)
    @mock.patch("tools.dev_env.upgrade_dev_env.verify_helm_min_version", return_value="v3.15.0")
    @mock.patch("shutil.which", return_value="/usr/bin/tool")
    def test_step_check_fails_missing_profile_values(
        self, _mock_which, _mock_helm, _mock_is_file, mock_log_error
    ):
        cfg = replace(
            self.config,
            profile_values="/tmp/nonexistent-profile-values-1429.yaml",
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_check(cfg)
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once_with(
            "Profile values file '/tmp/nonexistent-profile-values-1429.yaml' does not exist or is not a file."
        )

    @mock.patch(
        "tools.dev_env.upgrade_dev_env.execute_step",
        side_effect=FileNotFoundError("missing profile values"),
    )
    def test_run_pipeline_catches_oserror_and_quotes_profile_values_in_recovery_cmd(
        self, _mock_exec
    ):
        parsed = upgrade_dev_env.parse_args(
            [
                "--run-migrations",
                "--enable-components",
                "--profile-values",
                "/tmp/my dir/profile values.yaml",
                "--start-from",
                "app",
            ]
        )
        cfg = replace(
            self.config,
            run_migrations=True,
            profile_values="/tmp/my dir/profile values.yaml",
        )
        buf = io.StringIO()
        with redirect_stdout(buf), self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.run_pipeline(parsed, cfg, upgrade_dev_env.STEPS)
        self.assertEqual(cm.exception.code, 1)
        out = buf.getvalue()
        self.assertIn("--run-migrations", out)
        self.assertNotIn("--enable-components", out)
        self.assertIn("--profile-values '/tmp/my dir/profile values.yaml'", out)
        self.assertIn("--start-from app", out)


if __name__ == "__main__":
    unittest.main()
