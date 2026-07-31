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

"""Unit tests for tools.upgrade_dev_env."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools import upgrade_dev_env


class TestParseArgs(unittest.TestCase):
    """Tests for command-line argument parsing and mutual exclusion."""

    def test_defaults(self):
        args = upgrade_dev_env.parse_args([])
        self.assertEqual(args.config, "deploy/ctk_v2/xmanager-cloud-deployment.yaml")
        self.assertIsNone(args.start_from)
        self.assertIsNone(args.only)
        self.assertFalse(args.run_migrations)

    def test_custom_flags(self):
        args = upgrade_dev_env.parse_args(["-c", "custom.yaml", "-s", "images", "--run-migrations"])
        self.assertEqual(args.config, "custom.yaml")
        self.assertEqual(args.start_from, "images")
        self.assertTrue(args.run_migrations)

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

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_get_migration_status_dirty(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="Version: 1\nDirty: true\n", stderr=""
        )
        config = upgrade_dev_env.Config(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="e",
            namespace="n",
            registry="reg",
            acr_target="tar",
            gke_cluster="c",
            config_path="cfg",
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.get_migration_status(config, "ess", "inst", "db")
        self.assertEqual(cm.exception.code, 1)

    @mock.patch("tools.upgrade_dev_env.get_migration_status")
    def test_step_migrate_exits_code_10_when_pending(self, mock_status):
        mock_status.side_effect = [(1, 2), (1, 1)]
        config = upgrade_dev_env.Config(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="e",
            namespace="n",
            registry="reg",
            acr_target="tar",
            gke_cluster="c",
            config_path="cfg",
            run_migrations=False,
        )
        with self.assertRaises(SystemExit) as cm:
            upgrade_dev_env.step_migrate(config)
        self.assertEqual(cm.exception.code, 10)

    @mock.patch("tools.upgrade_dev_env.run_migration")
    @mock.patch("tools.upgrade_dev_env.get_migration_status")
    def test_step_migrate_runs_when_flag_enabled(self, mock_status, mock_run_mig):
        mock_status.side_effect = [(1, 2), (1, 3)]
        config = upgrade_dev_env.Config(
            project_id="p",
            deployment_name="d",
            region="r",
            environment="dev",
            namespace="n",
            registry="reg",
            acr_target="tar",
            gke_cluster="c",
            config_path="cfg",
            run_migrations=True,
        )
        upgrade_dev_env.step_migrate(config)
        self.assertEqual(mock_run_mig.call_count, 2)


class TestDeploymentResolution(unittest.TestCase):
    """Tests Kubernetes deployment name fallback resolution."""

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_resolve_exact_match(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertEqual(res, "actuator")

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_resolve_fallback(self, mock_run):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        ]
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertEqual(res, "actuator-deployment")

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_resolve_none_found(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=1, stdout="", stderr=""
        )
        res = upgrade_dev_env.resolve_deployment_name("actuator", "xmc")
        self.assertIsNone(res)


class TestStepSubprocesses(unittest.TestCase):
    """Tests hermetic subprocess calls per step."""

    def setUp(self):
        self.config = upgrade_dev_env.Config(
            project_id="proj-123",
            deployment_name="deploy-456",
            region="us-central1",
            environment="dev",
            namespace="xmc",
            registry="us-docker.pkg.dev/proj-123/xmc",
            acr_target="us-docker.pkg.dev/proj-123/xmc",
            gke_cluster="xmc-deploy-456-dev",
            config_path="deploy/ctk_v2/xmanager-cloud-deployment.yaml",
        )

    @mock.patch("shutil.which", return_value="/usr/bin/tool")
    @mock.patch.object(Path, "is_file", return_value=True)
    @mock.patch("os.access", return_value=True)
    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_check(self, mock_run, _mock_access, _mock_file, _mock_which):
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
        self.assertIn(
            mock.call(
                [
                    "gcloud",
                    "auth",
                    "configure-docker",
                    "us-docker.pkg.dev",
                    "--quiet",
                ]
            ),
            calls,
        )

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_images(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_images(self.config)
        self.assertEqual(mock_run.call_count, 8)

    @mock.patch("tools.upgrade_dev_env._propagate_compute_outputs")
    @mock.patch("tools.upgrade_dev_env._propagate_primary_outputs")
    @mock.patch.object(Path, "is_dir", return_value=True)
    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_app(self, mock_run, _mock_dir, _mock_primary, _mock_compute):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_app(self.config)
        # Verify gcluster create and deploy calls are present
        calls = [call_args[0][0] for call_args in mock_run.call_args_list]
        self.assertIn(
            [
                "./cluster-toolkit/gcluster",
                "create",
                "deploy/ctk_v2/xmanager-cloud.yaml",
                "-d",
                "deploy/ctk_v2/xmanager-cloud-deployment.yaml",
                "-o",
                "ctk-out",
                "-l",
                "WARNING",
                "-w",
                "--force",
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

    @mock.patch.object(Path, "is_dir", return_value=True)
    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_app_output_propagation(self, mock_run, _mock_dir):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        with tempfile.TemporaryDirectory() as tmp_dir:
            ctk_dir = Path(tmp_dir) / "ctk-out" / "deploy-456"
            ctk_dir.mkdir(parents=True)
            artifacts_dir = ctk_dir / ".ghpc" / "artifacts"
            artifacts_dir.mkdir(parents=True)
            (artifacts_dir / "primary_outputs.tfvars").write_text(
                'service_account_email_workload_service_account = "sa@proj.iam.gserviceaccount.com"\n'
                'service_account_email_health_service_gcp_sa = "hs-sa@proj.iam.gserviceaccount.com"\n',
                encoding="utf-8",
            )
            (artifacts_dir / "compute_outputs.tfvars").write_text(
                'cluster_endpoint = "10.0.0.1"\n',
                encoding="utf-8",
            )
            config = upgrade_dev_env.Config(
                environment="dev",
                deployment_name="deploy-456",
                project_id="proj-123",
                region="us-central1",
                namespace="xmc",
                registry="us-docker.pkg.dev/proj-123/xmc",
                acr_target="us-docker.pkg.dev/proj-123/xmc",
                gke_cluster="xmc-deploy-456-dev",
                config_path="/tmp/test_config.yaml",
            )
            with mock.patch(
                "tools.upgrade_dev_env.Path",
                side_effect=lambda p: ctk_dir if "ctk-out/deploy-456" in str(p) else Path(p),
            ):
                upgrade_dev_env.step_app(config)

            compute_inputs = ctk_dir / "compute" / "primary_inputs.auto.tfvars"
            app_inputs = ctk_dir / "xmc_app" / "primary_inputs.auto.tfvars"
            self.assertTrue(compute_inputs.exists())
            self.assertTrue(app_inputs.exists())
            self.assertIn(
                "service_account_email_health_service_gcp_sa",
                compute_inputs.read_text(encoding="utf-8"),
            )
            self.assertIn(
                "hs-sa@proj.iam.gserviceaccount.com",
                compute_inputs.read_text(encoding="utf-8"),
            )

    @mock.patch("tools.upgrade_dev_env.resolve_deployment_name")
    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_restart(self, mock_run, mock_resolve):
        mock_resolve.return_value = "actuator"
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_restart(self.config)
        self.assertEqual(mock_run.call_count, 7)

    @mock.patch("tools.upgrade_dev_env.run_cmd")
    def test_step_verify(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        )
        upgrade_dev_env.step_verify(self.config)
        mock_run.assert_called_once()
        _cmd, kwargs = mock_run.call_args
        self.assertEqual(
            kwargs["env"],
            {"ENVIRONMENT": "dev", "REGION": "us-central1", "PROJECT_ID": "proj-123"},
        )


if __name__ == "__main__":
    unittest.main()
