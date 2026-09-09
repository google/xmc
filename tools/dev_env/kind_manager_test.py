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
import urllib.error
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
        create_cmd = next(c[0][0] for c in mock_run.call_args_list if "create" in c[0][0])
        self.assertIn("--from-literal=jwt-secret=complex=base64=value==", create_cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_secret_generic_with_files_and_literals(self, mock_run: mock.MagicMock) -> None:
        self.manager._apply_secret_generic(
            "test-secret",
            ["xmc"],
            files=["key.json=/path/to/key.json"],
            literals=["client-secret=my-secret"],
        )
        create_cmd = next(c[0][0] for c in mock_run.call_args_list if "create" in c[0][0])
        self.assertIn("--from-file=key.json=/path/to/key.json", create_cmd)
        self.assertIn("--from-literal=client-secret=my-secret", create_cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_secret_generic_applies_helm_metadata(self, mock_run: mock.MagicMock) -> None:
        self.manager._apply_secret_generic(
            "test-secret",
            ["xmc"],
            literals=["a=b"],
        )
        annotate_cmd = next(c[0][0] for c in mock_run.call_args_list if "annotate" in c[0][0])
        self.assertIn("meta.helm.sh/release-name=xmc", annotate_cmd)
        self.assertIn("meta.helm.sh/release-namespace=xmc", annotate_cmd)
        self.assertIn("--overwrite", annotate_cmd)

        label_cmd = next(c[0][0] for c in mock_run.call_args_list if "label" in c[0][0])
        self.assertIn("app.kubernetes.io/managed-by=Helm", label_cmd)
        self.assertIn("--overwrite", label_cmd)

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

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_ensure_secrets_includes_tls_sans_and_least_privilege_oauth(
        self, mock_run: mock.MagicMock
    ) -> None:
        adc_file = Path(self.temp_dir.name) / "adc.json"
        adc_file.write_text("{}")
        with mock.patch.object(self.manager, "_resolve_adc_file", return_value=adc_file):
            self.manager.ensure_secrets()

        calls = [call[0][0] for call in mock_run.call_args_list]
        req_call = next(c for c in calls if "req" in c)
        self.assertIn("-addext", req_call)
        self.assertIn("subjectAltName = DNS:localhost, DNS:dex, IP:127.0.0.1", req_call)

        oauth_create = next(c for c in calls if "backend-oauth-secrets" in c and "create" in c)
        self.assertFalse(any("--from-file=key.json" in arg for arg in oauth_create))

    @mock.patch("tools.dev_env.remote_k8s_manager.setup_remote_k8s_secret")
    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_ensure_secrets_with_remote_k8s(
        self, mock_run: mock.MagicMock, mock_setup_remote: mock.MagicMock
    ) -> None:
        self.config.remote_k8s_context = "gke_remote_ctx"
        self.config.remote_k8s_namespace = "xmc-remote"
        self.config.remote_k8s_service_account = "test-sa"
        adc_file = Path(self.temp_dir.name) / "adc.json"
        adc_file.write_text("{}")
        with mock.patch.object(self.manager, "_resolve_adc_file", return_value=adc_file):
            self.manager.ensure_secrets()

        mock_setup_remote.assert_called_once_with(
            kind_context="kind-test-kind",
            remote_context="gke_remote_ctx",
            remote_namespace="xmc-remote",
            target_namespace="xmc",
            service_account="test-sa",
            secret_name="remote-k8s-config",
        )

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

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_manifests(self, mock_run: mock.MagicMock) -> None:
        manifests_dir = Path(self.temp_dir.name) / "manifests"
        manifests_dir.mkdir(parents=True, exist_ok=True)
        for mf in ["dummy_crds.yaml", "redis.yaml", "dex.yaml", "gateway_proxy.yaml"]:
            (manifests_dir / mf).write_text("apiVersion: v1\n")

        with (
            mock.patch.object(self.manager, "_resolve_manifests_dir", return_value=manifests_dir),
            mock.patch.object(self.manager, "repo_root", Path(self.temp_dir.name)),
        ):
            self.manager.apply_manifests()

        mock_run.assert_called_once()
        cmd = mock_run.call_args[0][0]
        self.assertIn("kubectl", cmd)
        self.assertIn("apply", cmd)

    def test_apply_manifests_missing_file_raises(self) -> None:
        manifests_dir = Path(self.temp_dir.name) / "nonexistent"
        with (
            mock.patch.object(self.manager, "_resolve_manifests_dir", return_value=manifests_dir),
            mock.patch.object(self.manager, "repo_root", Path(self.temp_dir.name) / "empty"),
        ):
            with self.assertRaises(FileNotFoundError):
                self.manager.apply_manifests()

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_apply_manifests_command_error_raises_value_error(
        self, mock_run: mock.MagicMock
    ) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["kubectl", "apply"])
        manifests_dir = Path(self.temp_dir.name) / "manifests"
        manifests_dir.mkdir(parents=True, exist_ok=True)
        for mf in ["dummy_crds.yaml", "redis.yaml", "dex.yaml", "gateway_proxy.yaml"]:
            (manifests_dir / mf).write_text("apiVersion: v1\n")

        with (
            mock.patch.object(self.manager, "_resolve_manifests_dir", return_value=manifests_dir),
            mock.patch.object(self.manager, "repo_root", Path(self.temp_dir.name)),
        ):
            with self.assertRaises(ValueError) as ctx:
                self.manager.apply_manifests()
            self.assertIn("Failed to apply supporting manifests", str(ctx.exception))

    @mock.patch("urllib.request.urlopen")
    def test_check_endpoint_health(self, mock_urlopen: mock.MagicMock) -> None:
        mock_resp = mock.MagicMock()
        mock_resp.status = 200
        mock_urlopen.return_value.__enter__.return_value = mock_resp
        self.assertTrue(self.manager.check_endpoint_health("http://localhost:8080/healthz"))

        mock_resp.status = 204
        self.assertTrue(self.manager.check_endpoint_health("http://localhost:8080/healthz"))

    @mock.patch("urllib.request.urlopen")
    def test_check_endpoint_health_failure(self, mock_urlopen: mock.MagicMock) -> None:
        mock_urlopen.side_effect = urllib.error.URLError("Connection refused")
        self.assertFalse(self.manager.check_endpoint_health("http://localhost:8080/healthz"))

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_get_status_running(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=["kubectl", "get", "pods"],
            returncode=0,
            stdout="pod-1 Running\n",
        )
        with (
            mock.patch.object(self.manager, "cluster_exists", return_value=True),
            mock.patch.object(self.manager, "check_endpoint_health", return_value=True),
        ):
            status = self.manager.get_status()

        self.assertTrue(status["running"])
        self.assertTrue(status["health"]["ui"])
        self.assertTrue(status["health"]["dex"])
        self.assertEqual(status["pods"]["xmc"], "pod-1 Running\n")
        self.assertEqual(mock_run.call_args[1].get("log_cmd"), False)

    def test_get_status_not_running(self) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=False):
            status = self.manager.get_status()
        self.assertFalse(status["running"])

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_get_status_pod_failure_handled(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=["kubectl", "get", "pods"],
            returncode=1,
            stdout="",
            stderr="error: context not found\n",
        )
        with (
            mock.patch.object(self.manager, "cluster_exists", return_value=True),
            mock.patch.object(self.manager, "check_endpoint_health", return_value=False),
        ):
            status = self.manager.get_status()

        self.assertTrue(status["running"])
        self.assertFalse(status["health"]["ui"])
        self.assertEqual(status["pods"]["xmc"], "error: context not found\n")
        self.assertEqual(mock_run.call_args[1].get("log_cmd"), False)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="reconciler", follow=True)

        cmd = mock_run.call_args[0][0]
        self.assertIn("app=reconciler", cmd)
        self.assertIn("-f", cmd)
        self.assertNotIn("--tail", " ".join(cmd))

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_follow_with_tail(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="reconciler", follow=True, lines=50)

        cmd = mock_run.call_args[0][0]
        self.assertIn("app=reconciler", cmd)
        self.assertIn("-f", cmd)
        self.assertIn("--tail=50", cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_zero_lines(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="ess", lines=0)

        cmd = mock_run.call_args[0][0]
        self.assertIn("--tail=0", cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_all_services(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name=None, lines=50)

        cmd = mock_run.call_args[0][0]
        self.assertIn("--tail=50", cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_keyboard_interrupt(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = KeyboardInterrupt()
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="reconciler", follow=True)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_cluster_not_running(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=False):
            self.manager.stream_logs(service_name="reconciler")
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_xid_service_rewrite(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="xid")
        cmd = mock_run.call_args[0][0]
        self.assertIn("app=xid-service", cmd)

    @mock.patch("tools.dev_env.kind_manager.run_command")
    def test_stream_logs_default_tail_when_not_follow(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(self.manager, "cluster_exists", return_value=True):
            self.manager.stream_logs(service_name="reconciler", follow=False, lines=None)
        cmd = mock_run.call_args[0][0]
        self.assertIn("--tail=100", cmd)

    def test_resolve_manifests_dir_first_candidate(self) -> None:
        first_candidate = self.manager.manifest_candidate_dirs[0]
        self.assertEqual(self.manager._resolve_manifests_dir(), first_candidate)


if __name__ == "__main__":
    unittest.main()
