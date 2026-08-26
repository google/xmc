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
        self.assertEqual(called_args.dns_project, "test-dns-proj")
        self.assertEqual(called_args.dns_zone, "test-dns-zone")
        self.assertEqual(called_args.start_from, "compile")
        self.assertIsNone(called_args.only)

    def test_gke_deploy_missing_required_args(self) -> None:
        """Verifies gke-deploy fails when required flags are omitted."""
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit):
                self.parser.parse_args(["gke-deploy", "--config", "test.yaml"])

    def test_gke_deploy_mutually_exclusive_flags(self) -> None:
        """Verifies gke-deploy exits if --start-from and --only are both set."""
        args = self.parser.parse_args(
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
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_gke_deploy(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("mutually exclusive", mock_log.call_args[0][0])

    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    def test_gke_upgrade_valid_args(self, mock_run: MagicMock) -> None:
        """Verifies gke-upgrade dispatches with valid arguments."""
        args = [
            "gke-upgrade",
            "--config",
            "deploy/ctk_v2/test.yaml",
            "--run-migrations",
            "--only",
            "images",
        ]
        parsed = self.parser.parse_args(args)
        parsed.func(parsed)
        mock_run.assert_called_once()
        called_args = mock_run.call_args[0][0]
        self.assertEqual(called_args.config, "deploy/ctk_v2/test.yaml")
        self.assertTrue(called_args.run_migrations)
        self.assertEqual(called_args.only, "images")
        self.assertIsNone(called_args.start_from)

    def test_gke_upgrade_mutually_exclusive_flags(self) -> None:
        """Verifies gke-upgrade exits if --start-from and --only are both set."""
        args = self.parser.parse_args(
            [
                "gke-upgrade",
                "--start-from",
                "migrate",
                "--only",
                "images",
            ]
        )
        with patch("tools.dev_env.dev_env.log_error") as mock_log:
            with self.assertRaises(SystemExit) as cm:
                dev_env.handle_gke_upgrade(args)
            self.assertEqual(cm.exception.code, 1)
            mock_log.assert_called_once()
            self.assertIn("mutually exclusive", mock_log.call_args[0][0])

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

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    @patch("logging.basicConfig")
    def test_main_dispatch_verbose(
        self, mock_logging: MagicMock, mock_upgrade: MagicMock, mock_venv: MagicMock
    ) -> None:
        """Verifies main() configures DEBUG logging on --verbose."""
        dev_env.main(["-v", "gke-upgrade"])
        mock_venv.assert_called_once()
        mock_logging.assert_called_once_with(level=logging.DEBUG)
        mock_upgrade.assert_called_once()

    @patch("tools.dev_env.dev_env.ensure_venv")
    @patch("tools.dev_env.upgrade_dev_env.run_upgrade")
    @patch("logging.basicConfig")
    def test_main_dispatch_quiet(
        self, mock_logging: MagicMock, mock_upgrade: MagicMock, mock_venv: MagicMock
    ) -> None:
        """Verifies main() configures ERROR logging on --quiet."""
        dev_env.main(["-q", "gke-upgrade"])
        mock_venv.assert_called_once()
        mock_logging.assert_called_once_with(level=logging.ERROR)
        mock_upgrade.assert_called_once()


if __name__ == "__main__":
    unittest.main()
