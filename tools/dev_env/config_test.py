#!/usr/bin/env python3
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

"""Unit tests for tools.dev_env.config module."""

import argparse
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tools.dev_env import config


class ConfigResolutionTest(unittest.TestCase):
    """Test cases for configuration path resolution and precedence loading."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()
        super().tearDown()

    def test_resolve_config_path_explicit(self) -> None:
        """Verifies explicitly specified configuration path is resolved."""
        cfg_file = self.root / "custom-config.yaml"
        cfg_file.write_text("vars:\n  project_id: test-p\n", encoding="utf-8")

        resolved = config.resolve_config_path(str(cfg_file), repo_root=self.root)
        self.assertEqual(resolved, cfg_file.resolve())

    def test_resolve_config_path_explicit_relative(self) -> None:
        """Verifies relative explicit path resolves against repo_root."""
        (self.root / "deploy" / "local").mkdir(parents=True, exist_ok=True)
        cfg_file = self.root / "deploy" / "local" / "custom.yaml"
        cfg_file.write_text("vars:\n  project_id: test-p\n", encoding="utf-8")

        resolved = config.resolve_config_path("deploy/local/custom.yaml", repo_root=self.root)
        self.assertEqual(resolved, cfg_file.resolve())

    def test_resolve_config_path_explicit_not_found(self) -> None:
        """Verifies FileNotFoundError is raised when explicit path does not exist."""
        with self.assertRaises(FileNotFoundError):
            config.resolve_config_path("non-existent.yaml", repo_root=self.root)

    def test_resolve_config_path_defaults(self) -> None:
        """Verifies default configuration locations prioritize deploy/local over deploy/ctk_v2."""
        local_cfg = self.root / "deploy" / "local" / "xmanager-cloud-local.yaml"
        local_cfg.parent.mkdir(parents=True, exist_ok=True)
        local_cfg.write_text("vars:\n  project_id: local-p\n", encoding="utf-8")

        ctk_cfg = self.root / "deploy" / "ctk_v2" / "xmanager-cloud-deployment.yaml"
        ctk_cfg.parent.mkdir(parents=True, exist_ok=True)
        ctk_cfg.write_text("vars:\n  project_id: ctk-p\n", encoding="utf-8")

        # When both exist, deploy/local is chosen
        resolved = config.resolve_config_path(repo_root=self.root)
        self.assertEqual(resolved, local_cfg.resolve())

        # When local is removed, fallback to ctk_v2
        local_cfg.unlink()
        resolved_fallback = config.resolve_config_path(repo_root=self.root)
        self.assertEqual(resolved_fallback, ctk_cfg.resolve())

    def test_resolve_config_path_none_when_empty(self) -> None:
        """Verifies None is returned when no default configuration files exist."""
        resolved = config.resolve_config_path(repo_root=self.root)
        self.assertIsNone(resolved)

    def test_resolve_config_path_empty_or_whitespace_string_fallback(self) -> None:
        """Verifies empty or whitespace config_path falls back to default locations."""
        ctk_cfg = self.root / "deploy" / "ctk_v2" / "xmanager-cloud-deployment.yaml"
        ctk_cfg.parent.mkdir(parents=True, exist_ok=True)
        ctk_cfg.write_text("vars:\n  project_id: ctk-p\n", encoding="utf-8")

        self.assertEqual(
            config.resolve_config_path("", repo_root=self.root),
            ctk_cfg.resolve(),
        )
        self.assertEqual(
            config.resolve_config_path("   ", repo_root=self.root),
            ctk_cfg.resolve(),
        )

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_precedence_cli(self) -> None:
        """Verifies CLI arguments take highest precedence over Env and YAML."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n",
            encoding="utf-8",
        )

        with patch.dict(
            "os.environ",
            {"PROJECT_ID": "env-p", "XMANAGER_ENV": "env-env"},
            clear=True,
        ):
            args = argparse.Namespace(
                config=str(cfg_file),
                project_id="cli-p",
                environment="cli-env",
                spanner_instance="cli-sp",
            )
            cfg = config.load_config(args, repo_root=self.root)
            self.assertEqual(cfg.project_id, "cli-p")
            self.assertEqual(cfg.environment, "cli-env")
            self.assertEqual(cfg.spanner_instance, "cli-sp")
            self.assertEqual(cfg.config_path, cfg_file.resolve())

    def test_load_config_precedence_env(self) -> None:
        """Verifies Environment variables override YAML variables and defaults."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n",
            encoding="utf-8",
        )

        with patch.dict(
            "os.environ",
            {"PROJECT_ID": "env-p", "XMANAGER_ENV": "env-env"},
            clear=True,
        ):
            args = argparse.Namespace(config=str(cfg_file))
            cfg = config.load_config(args, repo_root=self.root)
            self.assertEqual(cfg.project_id, "env-p")
            self.assertEqual(cfg.environment, "env-env")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_precedence_yaml(self) -> None:
        """Verifies YAML values are used when CLI and Env variables are unset."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n  cluster_name: custom-k8s\n",
            encoding="utf-8",
        )

        args = argparse.Namespace(config=str(cfg_file))
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "yaml-p")
        self.assertEqual(cfg.environment, "yaml-env")
        self.assertEqual(cfg.cluster_name, "custom-k8s")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_defaults(self) -> None:
        """Verifies default values when only project_id is provided via CLI."""
        args = argparse.Namespace(project_id="my-proj")
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "my-proj")
        self.assertEqual(cfg.environment, "dev")
        self.assertEqual(cfg.spanner_instance, "xmc-dev")
        self.assertEqual(cfg.ess_database, "experiment-db-dev")
        self.assertEqual(cfg.xid_database, "xids-db-dev")
        self.assertEqual(cfg.cluster_name, "xmc-dev")
        self.assertTrue(cfg.auto_migrate)
        self.assertEqual(
            cfg.enabled_services,
            [*config.BASE_SERVICES, "rod_data_provider", "health_service"],
        )
        self.assertEqual(cfg.gcp_service_account, "xmc-dev-sa@my-proj.iam.gserviceaccount.com")
        self.assertIsNone(cfg.config_path)

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_empty_strings_fallback_properly(self) -> None:
        """Verifies empty or whitespace values fallback cleanly without producing literal 'None'."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text("vars:\n  project_id: fallback-p\n", encoding="utf-8")

        args = argparse.Namespace(
            config=str(cfg_file),
            project_id="   ",
            environment="",
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "fallback-p")
        self.assertEqual(cfg.environment, "dev")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_missing_project_id_raises_error(self) -> None:
        """Verifies ValueError is raised when project_id cannot be resolved."""
        args = argparse.Namespace()
        with self.assertRaises(ValueError) as cm:
            config.load_config(args, repo_root=self.root)
        self.assertIn("Missing required configuration field: 'project_id'", str(cm.exception))

    @patch.dict("os.environ", {}, clear=True)
    def test_feature_flag_service_mappings(self) -> None:
        """Verifies feature flags conditionally expand enabled services."""
        args = argparse.Namespace(
            project_id="my-proj",
            enable_metrics=True,
            enable_components=True,
            rod_deployment_enabled=True,
            enable_health_service=True,
            enable_dashboard_service=True,
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertIn("ingestion_gateway", cfg.enabled_services)
        self.assertIn("components", cfg.enabled_services)
        self.assertIn("rod_data_provider", cfg.enabled_services)
        self.assertIn("health_service", cfg.enabled_services)
        self.assertIn("dashboard_service", cfg.enabled_services)

    @patch.dict("os.environ", {}, clear=True)
    def test_feature_flag_yaml_and_env_mappings(self) -> None:
        """Verifies feature flags resolved from YAML and environment variables."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  enable_metrics: true\n  health_service_deployment_enabled: true\n"
            "  rod_deployment_enabled: false\n",
            encoding="utf-8",
        )

        with patch.dict("os.environ", {"ENABLE_COMPONENTS": "true"}, clear=True):
            args = argparse.Namespace(config=str(cfg_file))
            cfg = config.load_config(args, repo_root=self.root)
            self.assertIn("ingestion_gateway", cfg.enabled_services)
            self.assertIn("components", cfg.enabled_services)
            self.assertIn("health_service", cfg.enabled_services)
            self.assertNotIn("rod_data_provider", cfg.enabled_services)
            self.assertNotIn("dashboard_service", cfg.enabled_services)

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_ingestion_gateway_disabled_overrides_enable_metrics(self) -> None:
        """Verifies ingestion_gateway_deployment_enabled: false suppresses ingestion_gateway even if enable_metrics is true."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  enable_metrics: true\n  ingestion_gateway_deployment_enabled: false\n",
            encoding="utf-8",
        )

        args = argparse.Namespace(config=str(cfg_file))
        cfg = config.load_config(args, repo_root=self.root)
        self.assertNotIn("ingestion_gateway", cfg.enabled_services)

    def test_remote_k8s_explicit_cli(self) -> None:
        """Verifies explicitly passing remote_k8s_context via CLI is resolved."""
        args = argparse.Namespace(
            project_id="test-p",
            remote_k8s_context="gke_proj_us-central1_cluster-1",
            remote_k8s_namespace="custom-ns",
            remote_k8s_service_account="custom-sa",
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.remote_k8s_context, "gke_proj_us-central1_cluster-1")
        self.assertEqual(cfg.remote_k8s_namespace, "custom-ns")
        self.assertEqual(cfg.remote_k8s_service_account, "custom-sa")

    def test_remote_k8s_auto_derive_from_flag(self) -> None:
        """Verifies boolean remote_k8s flag auto-derives GKE context name."""
        args = argparse.Namespace(
            project_id="test-proj",
            environment="dev",
            remote_k8s_context="true",
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(
            cfg.remote_k8s_context,
            "gke_test-proj_us-central1_xmc-xmc-v2-dev-dev",
        )
        self.assertEqual(cfg.remote_k8s_namespace, "xmc")
        self.assertEqual(cfg.remote_k8s_service_account, "actuator-service-account")

    def test_remote_k8s_explicit_context_parameter(self) -> None:
        """Verifies explicit remote_k8s_context parameter is preserved as-is."""
        args = argparse.Namespace(
            project_id="test-proj",
            remote_k8s_context="gke_test-proj_us-west1_my-workload-cluster",
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(
            cfg.remote_k8s_context,
            "gke_test-proj_us-west1_my-workload-cluster",
        )

    @patch.dict(
        "os.environ",
        {
            "PROJECT_ID": "env-p",
            "REMOTE_K8S_CONTEXT": "gke_env-p_us-central1_env-cluster",
        },
    )
    def test_remote_k8s_from_env(self) -> None:
        """Verifies REMOTE_K8S_CONTEXT environment variable resolves remote_k8s_context."""
        cfg = config.load_config(argparse.Namespace(), repo_root=self.root)
        self.assertEqual(cfg.remote_k8s_context, "gke_env-p_us-central1_env-cluster")

    def test_remote_k8s_from_yaml(self) -> None:
        """Verifies remote_k8s boolean in YAML enables remote K8s and derives context."""
        cfg_file = self.root / "remote_k8s_config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  remote_k8s: true\n  environment: dev\n",
            encoding="utf-8",
        )
        args = argparse.Namespace(config=str(cfg_file))
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(
            cfg.remote_k8s_context,
            "gke_yaml-p_us-central1_xmc-xmc-v2-dev-dev",
        )

    def test_mldiagnostics_and_xprof_bucket_resolution(self) -> None:
        """Verifies managed mldiagnostics and xprof bucket are resolved from YAML and env."""
        # YAML resolution
        cfg_file = self.root / "mldiag_config.yaml"
        cfg_file.write_text(
            "vars:\n"
            "  project_id: my-proj\n"
            "  enable_managed_mldiagnostics: true\n"
            "  managed_xprof_gcs_bucket: gs://yaml-bucket\n",
            encoding="utf-8",
        )
        args_yaml = argparse.Namespace(config=str(cfg_file))
        cfg_yaml = config.load_config(args_yaml, repo_root=self.root)
        self.assertTrue(cfg_yaml.enable_managed_mldiagnostics)
        self.assertEqual(cfg_yaml.managed_xprof_gcs_bucket, "gs://yaml-bucket")

        # Environment variable fallback resolution
        with patch.dict(
            "os.environ",
            {
                config.ENABLE_MANAGED_MLDIAGNOSTICS_ENV: "true",
                config.MANAGED_XPROF_GCS_BUCKET_ENV: "gs://env-bucket",
            },
            clear=True,
        ):
            args_env = argparse.Namespace(project_id="my-proj")
            cfg_env = config.load_config(args_env, repo_root=self.root)
            self.assertTrue(cfg_env.enable_managed_mldiagnostics)
            self.assertEqual(cfg_env.managed_xprof_gcs_bucket, "gs://env-bucket")

        # Default resolution when neither YAML nor env is set
        args_default = argparse.Namespace(project_id="my-proj")
        cfg_default = config.load_config(args_default, repo_root=self.root)
        self.assertFalse(cfg_default.enable_managed_mldiagnostics)
        self.assertEqual(cfg_default.managed_xprof_gcs_bucket, "")


class LocalDevConfigDataClassTest(unittest.TestCase):
    """Test cases for LocalDevConfig dataclass validation and dynamic naming."""

    def test_dynamic_naming_rules(self) -> None:
        """Verifies dynamic naming derives from environment string."""
        cfg = config.LocalDevConfig(project_id="test-proj", environment="staging")
        self.assertEqual(cfg.spanner_instance, "xmc-staging")
        self.assertEqual(cfg.ess_database, "experiment-db-staging")
        self.assertEqual(cfg.xid_database, "xids-db-staging")
        self.assertEqual(cfg.cluster_name, "xmc-staging")
        self.assertTrue(cfg.auto_migrate)

    def test_enabled_services_empty_list_preserved(self) -> None:
        """Verifies explicitly passing an empty list of services is preserved."""
        cfg = config.LocalDevConfig(project_id="test-proj", enabled_services=[])
        self.assertEqual(cfg.enabled_services, [])

    def test_enabled_services_default_factory(self) -> None:
        """Verifies default enabled_services returns a fresh copy of BASE_SERVICES."""
        cfg = config.LocalDevConfig(project_id="test-proj")
        self.assertEqual(cfg.enabled_services, list(config.BASE_SERVICES))
        self.assertIsNot(cfg.enabled_services, config.BASE_SERVICES)

    def test_managed_xprof_gcs_bucket_normalization(self) -> None:
        """Verifies GCS bucket trailing slashes and whitespace are normalized."""
        cfg = config.LocalDevConfig(
            project_id="test-proj",
            managed_xprof_gcs_bucket="  gs://my-bucket/  ",
        )
        self.assertEqual(cfg.managed_xprof_gcs_bucket, "gs://my-bucket")

    def test_invalid_managed_xprof_gcs_bucket_raises_value_error(self) -> None:
        """Verifies invalid managed_xprof_gcs_bucket raises ValueError."""
        invalid_buckets = ["invalid-bucket", "s3://my-bucket", "gs://", "gs:///", "gs:"]
        for bucket in invalid_buckets:
            with self.subTest(bucket=bucket):
                with self.assertRaises(ValueError):
                    config.LocalDevConfig(
                        project_id="test-proj",
                        managed_xprof_gcs_bucket=bucket,
                    )

    def test_empty_project_id_raises_value_error(self) -> None:
        """Verifies whitespace or empty project_id raises ValueError."""
        with self.assertRaises(ValueError):
            config.LocalDevConfig(project_id="")
        with self.assertRaises(ValueError):
            config.LocalDevConfig(project_id="   ")

    def test_gcp_service_account_remote_k8s_defaulting(self) -> None:
        """Verifies gcp_service_account defaults to workload SA in remote k8s or custom env."""
        cfg_remote = config.LocalDevConfig(
            project_id="test-p",
            environment="vluk-test",
            remote_k8s_context="gke_test-p_us-central1_test-cluster",
        )
        self.assertEqual(
            cfg_remote.gcp_service_account,
            "xmc-v2-vluk-test-xmc-wl-sa@test-p.iam.gserviceaccount.com",
        )

    def test_gcp_service_account_explicit_override(self) -> None:
        """Verifies explicit gcp_service_account overrides dynamic default."""
        cfg = config.LocalDevConfig(
            project_id="test-p",
            gcp_service_account="custom-sa@test-p.iam.gserviceaccount.com",
        )
        self.assertEqual(cfg.gcp_service_account, "custom-sa@test-p.iam.gserviceaccount.com")


class ResolveFlagsTest(unittest.TestCase):
    """Test cases for flag resolution helpers."""

    def test_resolve_str_flag_precedence(self) -> None:
        """Verifies string resolution precedence: CLI > Env > YAML > Default."""
        self.assertEqual(config._resolve_str_flag("cli", "env", "yaml", "def"), "cli")
        self.assertEqual(config._resolve_str_flag(None, "env", "yaml", "def"), "env")
        self.assertEqual(config._resolve_str_flag("", "env", "yaml", "def"), "env")
        self.assertEqual(config._resolve_str_flag("  ", "env", "yaml", "def"), "env")
        self.assertEqual(config._resolve_str_flag(None, None, "yaml", "def"), "yaml")
        self.assertEqual(config._resolve_str_flag(None, "", "yaml", "def"), "yaml")
        self.assertEqual(config._resolve_str_flag(None, None, None, "def"), "def")
        self.assertEqual(config._resolve_str_flag(None, None, "", "def"), "def")

    def test_resolve_bool_flag_precedence(self) -> None:
        """Verifies bool resolution precedence: CLI > Env > YAML > Default."""
        self.assertTrue(config._resolve_bool_flag(True, False, False, False))
        self.assertFalse(config._resolve_bool_flag(None, False, True, True))
        self.assertTrue(config._resolve_bool_flag(None, None, True, False))
        self.assertTrue(config._resolve_bool_flag(None, None, None, True))


if __name__ == "__main__":
    unittest.main()
