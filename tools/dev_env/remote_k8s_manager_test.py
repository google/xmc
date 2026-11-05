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

"""Unit tests for remote_k8s_manager module."""

import json
import subprocess
import unittest
from unittest.mock import MagicMock, patch

from tools.dev_env import remote_k8s_manager


class RemoteK8sManagerTest(unittest.TestCase):
    """Test suite for remote Kubernetes configuration extraction and secret setup."""

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_extract_remote_cluster_endpoint_and_ca_success(self, mock_run: MagicMock) -> None:
        payload = {
            "clusters": [
                {
                    "name": "test-cluster",
                    "cluster": {
                        "server": "https://203.0.113.10",
                        "certificate-authority-data": "dGVzdC1jYS1kYXRh",
                    },
                }
            ]
        }
        mock_run.return_value = MagicMock(
            stdout=json.dumps(payload),
            returncode=0,
        )

        server, ca_data = remote_k8s_manager.extract_remote_cluster_endpoint_and_ca("gke-test-ctx")
        self.assertEqual(server, "https://203.0.113.10")
        self.assertEqual(ca_data, "dGVzdC1jYS1kYXRh")

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_extract_remote_cluster_missing_clusters(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(
            stdout=json.dumps({"clusters": []}),
            returncode=0,
        )
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.extract_remote_cluster_endpoint_and_ca("gke-test-ctx")
        self.assertIn("No clusters found", str(ctx.exception))

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_extract_remote_cluster_missing_server(self, mock_run: MagicMock) -> None:
        payload = {
            "clusters": [
                {
                    "name": "test-cluster",
                    "cluster": {"certificate-authority-data": "abc"},
                }
            ]
        }
        mock_run.return_value = MagicMock(
            stdout=json.dumps(payload),
            returncode=0,
        )
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.extract_remote_cluster_endpoint_and_ca("gke-test-ctx")
        self.assertIn("endpoint missing", str(ctx.exception))

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_extract_remote_cluster_missing_ca(self, mock_run: MagicMock) -> None:
        payload = {
            "clusters": [
                {
                    "name": "test-cluster",
                    "cluster": {"server": "https://203.0.113.10"},
                }
            ]
        }
        mock_run.return_value = MagicMock(
            stdout=json.dumps(payload),
            returncode=0,
        )
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.extract_remote_cluster_endpoint_and_ca("gke-test-ctx")
        self.assertIn("certificate-authority-data missing", str(ctx.exception))

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_extract_remote_cluster_command_error(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["kubectl"])
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.extract_remote_cluster_endpoint_and_ca("gke-test-ctx")
        self.assertIn("Failed to query remote cluster configuration", str(ctx.exception))

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_mint_service_account_token_success(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(stdout=b"  jwt-token-xyz  \n", returncode=0)
        token = remote_k8s_manager.mint_service_account_token(
            context="gke-ctx",
            namespace="xmc",
            service_account="actuator-service-account",
            duration="12h",
        )
        self.assertEqual(token, "jwt-token-xyz")
        mock_run.assert_called_once_with(
            [
                "kubectl",
                "--context",
                "gke-ctx",
                "create",
                "token",
                "actuator-service-account",
                "-n",
                "xmc",
                "--duration=12h",
            ],
            check=True,
            capture_output=True,
            log_cmd=False,
        )

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_mint_service_account_token_empty(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(stdout="", returncode=0)
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.mint_service_account_token(
                context="gke-ctx",
                namespace="xmc",
            )
        self.assertIn("Token output was empty", str(ctx.exception))

    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_mint_service_account_token_error(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["kubectl"])
        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.mint_service_account_token(
                context="gke-ctx",
                namespace="xmc",
            )
        self.assertIn("Failed to mint token", str(ctx.exception))

    def test_generate_static_kubeconfig(self) -> None:
        cfg = remote_k8s_manager.generate_static_kubeconfig(
            server="https://10.0.0.1:443",
            ca_data="bWFzdGVyLWNh",
            token="static-bearer-tok",
            namespace="xmc-custom",
        )
        self.assertIn("server: https://10.0.0.1:443", cfg)
        self.assertIn("certificate-authority-data: bWFzdGVyLWNh", cfg)
        self.assertIn("token: static-bearer-tok", cfg)
        self.assertIn("namespace: xmc-custom", cfg)
        self.assertIn("current-context: remote-context", cfg)

    @patch("tools.dev_env.remote_k8s_manager.mint_service_account_token")
    @patch("tools.dev_env.remote_k8s_manager.extract_remote_cluster_endpoint_and_ca")
    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_setup_remote_k8s_secret_success(
        self,
        mock_run: MagicMock,
        mock_extract: MagicMock,
        mock_mint: MagicMock,
    ) -> None:
        mock_extract.return_value = ("https://203.0.113.10", "dGVzdC1jYQ==")
        mock_mint.return_value = "my-secret-token"

        remote_k8s_manager.setup_remote_k8s_secret(
            kind_context="kind-local",
            remote_context="gke-remote",
            remote_namespace="xmc",
            target_namespace="xmc",
            service_account="actuator-service-account",
            secret_name="remote-k8s-config",
        )

        mock_extract.assert_called_once_with("gke-remote")
        mock_mint.assert_called_once_with(
            "gke-remote",
            namespace="xmc",
            service_account="actuator-service-account",
        )

        # Verify create secret call was issued
        create_calls = [
            c
            for c in mock_run.call_args_list
            if len(c[0]) > 0
            and isinstance(c[0][0], list)
            and "create" in c[0][0]
            and "secret" in c[0][0]
        ]
        self.assertTrue(len(create_calls) >= 1)
        create_cmd = create_calls[0][0][0]
        self.assertIn("kubectl", create_cmd)
        self.assertIn("--context", create_cmd)
        self.assertIn("kind-local", create_cmd)
        self.assertIn("remote-k8s-config", create_cmd)
        self.assertTrue(any(arg.startswith("--from-literal=kubeconfig=") for arg in create_cmd))

    @patch("tools.dev_env.remote_k8s_manager.mint_service_account_token")
    @patch("tools.dev_env.remote_k8s_manager.extract_remote_cluster_endpoint_and_ca")
    @patch("tools.dev_env.remote_k8s_manager.run_command")
    def test_setup_remote_k8s_secret_failure(
        self,
        mock_run: MagicMock,
        mock_extract: MagicMock,
        mock_mint: MagicMock,
    ) -> None:
        mock_extract.return_value = ("https://203.0.113.10", "dGVzdC1jYQ==")
        mock_mint.return_value = "my-secret-token"

        def _side_effect(cmd: list[str], **kwargs: object) -> MagicMock:
            if "create" in cmd and "secret" in cmd:
                raise subprocess.CalledProcessError(1, cmd)
            return MagicMock(returncode=0)

        mock_run.side_effect = _side_effect

        with self.assertRaises(RuntimeError) as ctx:
            remote_k8s_manager.setup_remote_k8s_secret(
                kind_context="kind-local",
                remote_context="gke-remote",
            )
        self.assertIn("Failed to create secret 'remote-k8s-config'", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
