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

"""Unit tests for tools.dev_env.kind_manager."""

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.kind_manager import KindManager, check_preflight_tools


class KindManagerTest(unittest.TestCase):
    """Test suite for KindManager and preflight validation."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.config = LocalDevConfig(
            project_id="test-project",
            cluster_name="test-kind",
        )
        self.manager = KindManager(
            config=self.config,
            temp_dir=Path(self.temp_dir.name),
        )

    @mock.patch("shutil.which")
    def test_check_preflight_tools_success(self, mock_which: mock.MagicMock) -> None:
        mock_which.return_value = "/usr/bin/tool"
        check_preflight_tools(["docker", "kind"])
        self.manager.check_prerequisites(["docker", "kind"])

    @mock.patch("shutil.which")
    def test_check_preflight_tools_missing_raises_error(self, mock_which: mock.MagicMock) -> None:
        mock_which.side_effect = lambda tool: None if tool == "kind" else "/usr/bin/" + tool
        with self.assertRaises(RuntimeError) as ctx:
            check_preflight_tools(["docker", "kind"])
        self.assertIn("kind", str(ctx.exception))

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_exists_true(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=["kind", "get", "clusters"],
            returncode=0,
            stdout="other-cluster\ntest-kind\n",
        )
        self.assertTrue(self.manager.exists())
        self.assertTrue(self.manager.cluster_exists("test-kind"))

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_exists_false(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=["kind", "get", "clusters"],
            returncode=0,
            stdout="other-cluster\n",
        )
        self.assertFalse(self.manager.exists())
        self.assertFalse(self.manager.cluster_exists("test-kind"))

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_exists_command_failure(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=["kind", "get", "clusters"],
            returncode=1,
            stdout="",
            stderr="error",
        )
        self.assertFalse(self.manager.exists())

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_create_cluster_new(self, mock_run: mock.MagicMock) -> None:
        with (
            mock.patch.object(self.manager, "cluster_exists", return_value=False),
            mock.patch.object(
                self.manager,
                "_resolve_manifests_dir",
                return_value=Path(self.temp_dir.name),
            ),
        ):
            kind_cfg = Path(self.temp_dir.name) / "kind_config.yaml"
            kind_cfg.write_text("kind: Cluster\n")
            self.manager.create_cluster(recreate=False)

        mock_run.assert_any_call(
            [
                "kind",
                "create",
                "cluster",
                "--name",
                "test-kind",
                f"--config={kind_cfg}",
            ]
        )
        mock_run.assert_any_call(["kubectl", "config", "use-context", "kind-test-kind"])

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_create_cluster_recreate(self, mock_run: mock.MagicMock) -> None:
        with (
            mock.patch.object(self.manager, "cluster_exists", side_effect=[True, False]),
            mock.patch.object(self.manager, "delete_cluster") as mock_delete,
            mock.patch.object(
                self.manager,
                "_resolve_manifests_dir",
                return_value=Path(self.temp_dir.name),
            ),
        ):
            kind_cfg = Path(self.temp_dir.name) / "kind_config.yaml"
            kind_cfg.write_text("kind: Cluster\n")
            self.manager.create_cluster(recreate=True)
            mock_delete.assert_called_once_with("test-kind")

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_create_cluster_already_exists_no_recreate(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.create_cluster(recreate=False)
        mock_run.assert_called_once_with(["kubectl", "config", "use-context", "kind-test-kind"])

    def test_create_cluster_missing_config_raises(self) -> None:
        with (
            mock.patch.object(self.manager, "cluster_exists", return_value=False),
            mock.patch.object(
                self.manager,
                "_resolve_manifests_dir",
                return_value=Path(self.temp_dir.name),
            ),
        ):
            with self.assertRaises(FileNotFoundError):
                self.manager.create_cluster()

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_create_secrets_success(self, mock_run: mock.MagicMock) -> None:
        adc_file = Path(self.temp_dir.name) / "adc.json"
        adc_file.write_text("{}")
        with mock.patch.object(self.manager, "_resolve_adc_file", return_value=adc_file):
            self.manager.ensure_secrets()

        self.assertTrue(mock_run.called)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_create_secrets_alias(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "ensure_secrets") as mock_ensure:
            self.manager.create_secrets("custom-cluster")
            mock_ensure.assert_called_once_with("custom-cluster")

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_delete_cluster(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.delete_cluster()

        mock_run.assert_called_once_with(
            [
                "kind",
                "delete",
                "cluster",
                "--name",
                "test-kind",
            ]
        )

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_delete_cluster_not_exists(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=False):
            self.manager.delete_cluster()

        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_secret_generic_splits_first_equal_only(self, mock_run: mock.MagicMock) -> None:
        self.manager._apply_secret_generic(
            "test-secret",
            ["xmc"],
            literals=["jwt-secret=complex=base64=value=="],
        )
        create_cmd = mock_run.call_args[0][0]
        self.assertIn("--from-literal=jwt-secret=complex=base64=value==", create_cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_secret_generic_with_files_and_literals(self, mock_run: mock.MagicMock) -> None:
        self.manager._apply_secret_generic(
            "test-secret",
            ["xmc"],
            files=["key.json=/path/to/key.json"],
            literals=["client-secret=my-secret"],
        )
        create_cmd = mock_run.call_args[0][0]
        self.assertIn("--from-file=key.json=/path/to/key.json", create_cmd)
        self.assertIn("--from-literal=client-secret=my-secret", create_cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_secret_generic_failure_raises_runtime_error(
        self, mock_run: mock.MagicMock
    ) -> None:
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0),
            subprocess.CalledProcessError(returncode=1, cmd=["kubectl"]),
        ]
        with self.assertRaises(RuntimeError) as ctx:
            self.manager._apply_secret_generic("test-secret", ["xmc"], literals=["a=b"])
        self.assertIn(
            "Failed to create secret 'test-secret' in namespace 'xmc'", str(ctx.exception)
        )

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_ensure_secrets_restricts_tls_key_permissions(self, mock_run: mock.MagicMock) -> None:
        adc_file = Path(self.temp_dir.name) / "adc.json"
        adc_file.write_text("{}")
        tls_key = Path(self.temp_dir.name) / "dev-validation.key"

        def create_key_side_effect(cmd: list[str], **kwargs: object) -> subprocess.CompletedProcess:
            if "genpkey" in cmd:
                tls_key.write_text("dummy-key")
            return subprocess.CompletedProcess(args=cmd, returncode=0)

        mock_run.side_effect = create_key_side_effect
        with mock.patch.object(self.manager, "_resolve_adc_file", return_value=adc_file):
            self.manager.ensure_secrets()

        self.assertEqual(oct(tls_key.stat().st_mode & 0o777), oct(0o600))

    def test_default_temp_dir_under_home(self) -> None:
        km = KindManager(config=self.config)
        self.assertEqual(km.temp_dir, Path.home() / ".xmc" / "dev" / "test-kind")

    def test_invalid_cluster_name_raises_error(self) -> None:
        invalid_config = LocalDevConfig(
            project_id="test-project",
            cluster_name="../invalid-name",
        )
        with self.assertRaises(ValueError) as ctx:
            KindManager(config=invalid_config)
        self.assertIn("Invalid cluster name", str(ctx.exception))

    def test_resolve_adc_file_from_env(self) -> None:
        adc_file = Path(self.temp_dir.name) / "custom_adc.json"
        adc_file.write_text("{}")
        with mock.patch.dict("os.environ", {"GOOGLE_APPLICATION_CREDENTIALS": str(adc_file)}):
            resolved = self.manager._resolve_adc_file()
            self.assertEqual(resolved, adc_file)

    def test_resolve_adc_file_from_spanner_emulator(self) -> None:
        with (
            mock.patch.dict("os.environ", {"SPANNER_EMULATOR_HOST": "localhost:9010"}, clear=True),
            mock.patch("pathlib.Path.home", return_value=Path(self.temp_dir.name)),
        ):
            resolved = self.manager._resolve_adc_file()
            self.assertTrue(resolved.exists())
            self.assertIn("dummy", resolved.read_text())

    def test_resolve_adc_file_missing_raises_error(self) -> None:
        with (
            mock.patch.dict("os.environ", {}, clear=True),
            mock.patch("pathlib.Path.home", return_value=Path(self.temp_dir.name)),
        ):
            with self.assertRaises(FileNotFoundError):
                self.manager._resolve_adc_file()


if __name__ == "__main__":
    unittest.main()
