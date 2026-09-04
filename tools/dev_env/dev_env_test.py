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

"""Unit tests for dev_env.py CLI."""

import io
import logging
import unittest
from unittest.mock import MagicMock, patch

from tools.dev_env import dev_env
from tools.dev_env.config import LocalDevConfig


class DevEnvCliTest(unittest.TestCase):
    """Test cases for the unified dev_env CLI entrypoint."""

    def setUp(self) -> None:
        super().setUp()
        self.parser = dev_env.create_parser()

    def test_parser_help_output(self) -> None:
        """Verifies top-level help text lists active subcommands and options."""
        help_stream = io.StringIO()
        with self.assertRaises(SystemExit) as cm:
            with patch("sys.stdout", help_stream):
                self.parser.parse_args(["--help"])
        self.assertEqual(cm.exception.code, 0)
        output = help_stream.getvalue()
        self.assertIn("gke-deploy", output)
        self.assertIn("gke-upgrade", output)
        self.assertIn("deploy", output)
        self.assertIn("upgrade", output)
        self.assertIn("cluster", output)
        self.assertIn("status", output)
        self.assertIn("logs", output)
        self.assertIn("--verbose", output)
        self.assertIn("--quiet", output)
        self.assertIn("--version", output)
        self.assertIn("Examples:", output)

    def test_parser_version_output(self) -> None:
        """Verifies --version flag outputs version and exits cleanly."""
        version_stream = io.StringIO()
        with self.assertRaises(SystemExit) as cm:
            with patch("sys.stdout", version_stream):
                self.parser.parse_args(["--version"])
        self.assertEqual(cm.exception.code, 0)
        self.assertIn("dev_env", version_stream.getvalue())

    def test_main_no_args_prints_help_and_exits(self) -> None:
        """Verifies running main with empty args prints help and exits cleanly."""
        with patch.object(dev_env, "ensure_venv"):
            with patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
                with self.assertRaises(SystemExit) as cm:
                    dev_env.main([])
                self.assertEqual(cm.exception.code, 0)
                self.assertIn("XMC Development Environment", mock_stdout.getvalue())

    @patch("tools.dev_env.deploy_dev_env.run_deployment")
    def test_gke_deploy_valid_args(self, mock_run: MagicMock) -> None:
        """Verifies gke-deploy dispatches with valid arguments."""
        args = [
            "gke-deploy",
            "--config",
            "deploy/ctk_v2/test.yaml",
            "--project-id",
            "test-p",
            "--environment",
            "staging",
            "--spanner-instance",
            "test-sp",
            "--ess-db",
            "test-ess",
            "--xid-db",
            "test-xid",
            "--cluster-name",
            "test-cluster",
            "--auto-migrate",
            "--enable-metrics",
            "--enable-managed-mldiagnostics",
            "--managed-xprof-gcs-bucket",
            "gs://test-bucket",
            "--dns-project",
            "test-dns-proj",
            "--dns-zone",
            "test-dns-zone",
            "--start-from",
            "compile",
        ]
        parsed = self.parser.parse_args(args)
        parsed.func(parsed)
        mock_run.assert_called_once()
        called_args = mock_run.call_args[0][0]
        self.assertEqual(called_args.config, "deploy/ctk_v2/test.yaml")
        self.assertEqual(called_args.project_id, "test-p")
        self.assertEqual(called_args.environment, "staging")
        self.assertEqual(called_args.spanner_instance, "test-sp")
        self.assertEqual(called_args.ess_database, "test-ess")
        self.assertEqual(called_args.xid_database, "test-xid")
        self.assertEqual(called_args.cluster_name, "test-cluster")
        self.assertTrue(called_args.auto_migrate)
        self.assertTrue(called_args.enable_metrics)
        self.assertTrue(called_args.enable_managed_mldiagnostics)
        self.assertEqual(called_args.managed_xprof_gcs_bucket, "gs://test-bucket")
        self.assertEqual(called_args.dns_project, "test-dns-proj")
        self.assertEqual(called_args.dns_zone, "test-dns-zone")
        self.assertEqual(called_args.start_from, "compile")
        self.assertIsNone(called_args.only)

    def test_gke_deploy_missing_required_args(self) -> None:
        """Verifies gke-deploy fails when required flags are omitted."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit):
                self.parser.parse_args(["gke-deploy", "--config", "test.yaml"])

    def test_missing_subcommand_with_global_flags_fails(self) -> None:
        """Verifies parsing fails with non-zero exit code when subcommand is missing."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as cm:
                self.parser.parse_args(["--verbose"])
            self.assertEqual(cm.exception.code, 2)

    def test_gke_deploy_mutually_exclusive_flags(self) -> None:
        """Verifies gke-deploy exits if --start-from and --only are both set."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as cm:
                self.parser.parse_args(
                    [
                        "gke-deploy",
                        "--config",
                        "test.yaml",
                        "--dns-project",
                        "p",
                        "--dns-zone",
                        "z",
                        "--start-from",
                        "apis",
                        "--only",
                        "primary",
                    ]
                )
            self.assertEqual(cm.exception.code, 2)

    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    def test_gke_upgrade_valid_args(self, mock_run: MagicMock) -> None:
        """Verifies gke-upgrade dispatches with valid arguments and override flags."""
        args = [
            "gke-upgrade",
            "--config",
            "deploy/ctk_v2/test.yaml",
            "--project-id",
            "test-p",
            "--environment",
            "staging",
            "--no-auto-migrate",
            "--run-migrations",
            "--only",
            "images",
        ]
        parsed = self.parser.parse_args(args)
        parsed.func(parsed)
        mock_run.assert_called_once()
        called_args = mock_run.call_args[0][0]
        self.assertEqual(called_args.config, "deploy/ctk_v2/test.yaml")
        self.assertEqual(called_args.project_id, "test-p")
        self.assertEqual(called_args.environment, "staging")
        self.assertFalse(called_args.auto_migrate)
        self.assertTrue(called_args.run_migrations)
        self.assertEqual(called_args.only, "images")
        self.assertIsNone(called_args.start_from)

    def test_gke_upgrade_mutually_exclusive_flags(self) -> None:
        """Verifies gke-upgrade exits if --start-from and --only are both set."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as cm:
                self.parser.parse_args(
                    [
                        "gke-upgrade",
                        "--start-from",
                        "migrate",
                        "--only",
                        "images",
                    ]
                )
            self.assertEqual(cm.exception.code, 2)

    @patch("tools.dev_env.dev_env.handle_gke_deploy")
    def test_deploy_target_gke_success(self, mock_handle_gke: MagicMock) -> None:
        """Verifies generic deploy command with target=gke forwards to handle_gke_deploy."""
        args = self.parser.parse_args(
            [
                "deploy",
                "--target",
                "gke",
                "--config",
                "deploy.yaml",
                "--dns-project",
                "my-dns",
                "--dns-zone",
                "my-zone",
            ]
        )
        dev_env.handle_deploy(args)
        mock_handle_gke.assert_called_once_with(args)

    def test_deploy_target_gke_missing_dns(self) -> None:
        """Verifies generic deploy with target=gke fails when DNS args are missing."""
        args = self.parser.parse_args(
            [
                "deploy",
                "--target",
                "gke",
                "--config",
                "deploy.yaml",
            ]
        )
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_deploy(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("--dns-project and --dns-zone", mock_log.call_args[0][0])

    def test_deploy_target_gke_missing_config(self) -> None:
        """Verifies generic deploy with target=gke fails when --config is missing."""
        args = self.parser.parse_args(
            [
                "deploy",
                "--target",
                "gke",
                "--dns-project",
                "p",
                "--dns-zone",
                "z",
            ]
        )
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_deploy(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("--config is required", mock_log.call_args[0][0])

    def test_deploy_unsupported_target(self) -> None:
        """Verifies generic deploy fails gracefully for unsupported target."""
        args = self.parser.parse_args(
            [
                "deploy",
                "--config",
                "deploy.yaml",
                "--dns-project",
                "p",
                "--dns-zone",
                "z",
            ]
        )
        args.target = "unknown"
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_deploy(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("Unsupported deployment target", mock_log.call_args[0][0])

    def test_deploy_mutually_exclusive_flags(self) -> None:
        """Verifies deploy exits if --start-from and --only are both set."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as cm:
                self.parser.parse_args(
                    [
                        "deploy",
                        "--start-from",
                        "apis",
                        "--only",
                        "primary",
                    ]
                )
            self.assertEqual(cm.exception.code, 2)

    @patch("tools.dev_env.dev_env.handle_gke_upgrade")
    def test_upgrade_target_gke(self, mock_handle_gke: MagicMock) -> None:
        """Verifies generic upgrade with target=gke forwards to handle_gke_upgrade."""
        args = self.parser.parse_args(["upgrade", "--target", "gke"])
        dev_env.handle_upgrade(args)
        mock_handle_gke.assert_called_once_with(args)

    def test_upgrade_unsupported_target(self) -> None:
        """Verifies generic upgrade fails gracefully for unsupported target."""
        args = self.parser.parse_args(["upgrade"])
        args.target = "unknown"
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_upgrade(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("Unsupported upgrade target", mock_log.call_args[0][0])

    def test_upgrade_mutually_exclusive_flags(self) -> None:
        """Verifies upgrade exits if --start-from and --only are both set."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as cm:
                self.parser.parse_args(
                    [
                        "upgrade",
                        "--start-from",
                        "migrate",
                        "--only",
                        "images",
                    ]
                )
            self.assertEqual(cm.exception.code, 2)

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    @patch("logging.basicConfig")
    def test_main_dispatch_default_logging(
        self, mock_logging: MagicMock, mock_upgrade: MagicMock, mock_venv: MagicMock
    ) -> None:
        """Verifies main() configures INFO logging by default."""
        dev_env.main(["gke-upgrade"])
        mock_venv.assert_called_once()
        mock_logging.assert_called_once_with(level=logging.INFO, format="%(message)s")
        mock_upgrade.assert_called_once()

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    @patch("logging.basicConfig")
    def test_main_dispatch_verbose(
        self,
        mock_logging: MagicMock,
        mock_upgrade: MagicMock,
        mock_venv: MagicMock,
    ) -> None:
        """Verifies main() configures DEBUG logging on --verbose."""
        dev_env.main(["-v", "gke-upgrade"])
        mock_venv.assert_called_once()
        mock_logging.assert_called_once_with(
            level=logging.DEBUG,
            format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        )
        mock_upgrade.assert_called_once()

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    @patch("logging.basicConfig")
    def test_main_dispatch_quiet(
        self,
        mock_logging: MagicMock,
        mock_upgrade: MagicMock,
        mock_venv: MagicMock,
    ) -> None:
        """Verifies main() configures ERROR logging on --quiet."""
        dev_env.main(["-q", "gke-upgrade"])
        mock_venv.assert_called_once()
        mock_logging.assert_called_once_with(
            level=logging.ERROR,
            format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        )
        mock_upgrade.assert_called_once()

    @patch("tools.dev_env.spanner_manager.SpannerManager.ensure_databases_exist")
    @patch("tools.dev_env.config.load_config")
    def test_spanner_create_action(
        self, mock_load_config: MagicMock, mock_ensure: MagicMock
    ) -> None:
        """Verifies 'dev_env spanner create' dispatches to SpannerManager.ensure_databases_exist."""
        args = self.parser.parse_args(["spanner", "create", "--project-id", "my-p"])
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_ensure.assert_called_once()

    @patch("tools.dev_env.spanner_manager.SpannerManager.apply_migrations")
    @patch("tools.dev_env.config.load_config")
    def test_spanner_migrate_action(
        self, mock_load_config: MagicMock, mock_migrate: MagicMock
    ) -> None:
        """Verifies 'dev_env spanner migrate' dispatches to SpannerManager.apply_migrations."""
        args = self.parser.parse_args(["spanner", "migrate", "--project-id", "my-p"])
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_migrate.assert_called_once_with(force=True)

    @patch("tools.dev_env.spanner_manager.SpannerManager.drop_databases")
    @patch("tools.dev_env.config.load_config")
    def test_spanner_drop_action(self, mock_load_config: MagicMock, mock_drop: MagicMock) -> None:
        """Verifies 'dev_env spanner drop' dispatches to SpannerManager.drop_databases."""
        args = self.parser.parse_args(["spanner", "drop", "--project-id", "my-p"])
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_drop.assert_called_once()

    @patch("tools.dev_env.config.load_config")
    @patch("tools.dev_env.dev_env.log_error")
    def test_spanner_unknown_action(
        self, mock_log_error: MagicMock, mock_load_config: MagicMock
    ) -> None:
        """Verifies unknown spanner action exits with error code 1."""
        args = self.parser.parse_args(["spanner"])
        args.spanner_action = "invalid"
        with self.assertRaises(SystemExit) as cm:
            dev_env.handle_spanner(args)
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once()

    @patch("tools.dev_env.kind_manager.KindManager.apply_manifests")
    @patch("tools.dev_env.kind_manager.KindManager.ensure_secrets")
    @patch("tools.dev_env.kind_manager.KindManager.create_cluster")
    @patch("tools.dev_env.kind_manager.KindManager.check_prerequisites")
    @patch("tools.dev_env.config.load_config")
    def test_cluster_create_action(
        self,
        mock_load_config: MagicMock,
        mock_check_pre: MagicMock,
        mock_create: MagicMock,
        mock_ensure: MagicMock,
        mock_apply: MagicMock,
    ) -> None:
        """Verifies 'dev_env cluster create' executes preflight, cluster creation, secrets, and manifests."""
        args = self.parser.parse_args(
            ["cluster", "create", "--cluster-name", "my-kind", "--recreate"]
        )
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_check_pre.assert_called_once()
        mock_create.assert_called_once_with(recreate=True)
        mock_ensure.assert_called_once()
        mock_apply.assert_called_once()

    @patch("tools.dev_env.kind_manager.KindManager.delete_cluster")
    @patch("tools.dev_env.config.load_config")
    def test_cluster_delete_action(
        self,
        mock_load_config: MagicMock,
        mock_delete: MagicMock,
    ) -> None:
        """Verifies 'dev_env cluster delete' dispatches to KindManager.delete_cluster."""
        mock_load_config.return_value = LocalDevConfig(
            project_id="test-p",
            cluster_name="my-kind",
        )
        args = self.parser.parse_args(["cluster", "delete", "--cluster-name", "my-kind"])
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_delete.assert_called_once()

    @patch("tools.dev_env.config.load_config")
    @patch("tools.dev_env.dev_env.log_error")
    def test_cluster_unknown_action(
        self,
        mock_log_error: MagicMock,
        mock_load_config: MagicMock,
    ) -> None:
        """Verifies unknown cluster action exits with error code 1."""
        mock_load_config.return_value = LocalDevConfig(
            project_id="test-p",
            cluster_name="my-kind",
        )
        args = self.parser.parse_args(["cluster"])
        args.cluster_action = "invalid"
        with self.assertRaises(SystemExit) as cm:
            dev_env.handle_cluster(args)
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once()

    @patch("tools.dev_env.kind_manager.KindManager.get_status")
    @patch("tools.dev_env.config.load_config")
    def test_status_action(
        self,
        mock_load_config: MagicMock,
        mock_get_status: MagicMock,
    ) -> None:
        """Verifies 'dev_env status' dispatches to KindManager.get_status."""
        args = self.parser.parse_args(["status", "--cluster-name", "my-kind"])
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_get_status.assert_called_once()

    @patch("tools.dev_env.kind_manager.KindManager.stream_logs")
    @patch("tools.dev_env.config.load_config")
    def test_logs_action(
        self,
        mock_load_config: MagicMock,
        mock_stream_logs: MagicMock,
    ) -> None:
        """Verifies 'dev_env logs' dispatches to KindManager.stream_logs."""
        args = self.parser.parse_args(
            ["logs", "reconciler", "--follow", "--tail", "50", "-n", "custom-ns"]
        )
        args.func(args)
        mock_load_config.assert_called_once_with(args)
        mock_stream_logs.assert_called_once_with(
            service_name="reconciler",
            follow=True,
            lines=50,
            namespace="custom-ns",
        )

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.dev_env.log_error")
    @patch(
        "tools.dev_env.upgrade_dev_env.run_upgrade",
        side_effect=ValueError("Invalid config parameter"),
    )
    def test_main_handles_value_error(
        self, mock_upgrade: MagicMock, mock_log_error: MagicMock, mock_venv: MagicMock
    ) -> None:
        """Verifies main() handles ValueError gracefully and exits with code 1."""
        with self.assertRaises(SystemExit) as cm:
            dev_env.main(["gke-upgrade"])
        self.assertEqual(cm.exception.code, 1)
        mock_log_error.assert_called_once_with("Invalid config parameter")

    def test_parser_service_feature_flags(self) -> None:
        """Verifies health-service and dashboard-service flags parse into namespace."""
        parsed = self.parser.parse_args(
            ["gke-upgrade", "--enable-health-service", "--enable-dashboard-service"]
        )
        self.assertTrue(parsed.enable_health_service)
        self.assertTrue(parsed.enable_dashboard_service)

        parsed_neg = self.parser.parse_args(
            ["gke-upgrade", "--no-enable-health-service", "--no-enable-dashboard-service"]
        )
        self.assertFalse(parsed_neg.enable_health_service)
        self.assertFalse(parsed_neg.enable_dashboard_service)

    def test_parser_mldiagnostics_feature_flags(self) -> None:
        """Verifies mldiagnostics flags and negative flags parse into namespace."""
        parsed = self.parser.parse_args(
            [
                "gke-upgrade",
                "--enable-managed-mldiagnostics",
                "--managed-xprof-gcs-bucket",
                "gs://my-bucket",
            ]
        )
        self.assertTrue(parsed.enable_managed_mldiagnostics)
        self.assertEqual(parsed.managed_xprof_gcs_bucket, "gs://my-bucket")

        parsed_neg = self.parser.parse_args(
            ["gke-upgrade", "--no-enable-managed-mldiagnostics"]
        )
        self.assertFalse(parsed_neg.enable_managed_mldiagnostics)


if __name__ == "__main__":
    unittest.main()
