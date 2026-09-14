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

"""Unit tests for example_runner module."""

from __future__ import annotations

import json
import os
import socket
import subprocess
import unittest
from unittest.mock import MagicMock, patch

from test.e2e.examples.example_runner import (
    ADMIN_USER,
    ExampleLaunchResult,
    execute_example_launch,
    is_port_open,
    parse_launch_output,
    port_forward_grpc_service,
)
from test.e2e.lib.dev_token import fetch_dex_oidc_token


class TestExampleRunner(unittest.TestCase):
    """Tests for reusable example runner functions and classes."""

    def test_example_launch_result_properties(self) -> None:
        res = ExampleLaunchResult(
            xid="12345",
            wid="1",
            artifact_name="experiments/12345/workUnits/1/artifacts/678",
            raw_output="test output",
        )
        self.assertEqual(res.experiment_name, "experiments/12345")
        self.assertEqual(res.canonical_work_unit_name, "experiments/12345/workUnits/1")
        self.assertEqual(res.artifact_name, "experiments/12345/workUnits/1/artifacts/678")

        res_no_wid = ExampleLaunchResult(xid="999", wid=None)
        self.assertEqual(res_no_wid.experiment_name, "experiments/999")
        self.assertIsNone(res_no_wid.canonical_work_unit_name)

    def test_is_port_open(self) -> None:
        # Binding a socket makes it open
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.bind(("127.0.0.1", 0))
            s.listen(1)
            port = s.getsockname()[1]
            self.assertTrue(is_port_open("127.0.0.1", port))

        # Closed socket port should return False
        self.assertFalse(is_port_open("127.0.0.1", port))

    @patch("urllib.request.urlopen")
    def test_fetch_dex_oidc_token(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps(
            {
                "id_token": "mock.dex.rs256.jwt",
            }
        ).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_response

        token = fetch_dex_oidc_token(email="user@test.local")
        self.assertEqual(token, "mock.dex.rs256.jwt")

    def test_parse_launch_output_success(self) -> None:
        sample_output = """
Creating validation experiment with a Chart artifact on a WorkUnit
Created Work Unit successfully: experiments/42/workUnits/7/operations/op-1
Created Chart Artifact successfully: experiments/42/workUnits/7/artifacts/101
Experiment Dashboard URL: https://localhost:8080/experiments/42
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "42")
        self.assertEqual(result.wid, "7")
        self.assertEqual(result.artifact_name, "experiments/42/workUnits/7/artifacts/101")
        self.assertEqual(result.experiment_name, "experiments/42")
        self.assertEqual(result.canonical_work_unit_name, "experiments/42/workUnits/7")

    def test_parse_launch_output_fails_on_empty(self) -> None:
        with self.assertRaises(RuntimeError):
            parse_launch_output("no experiment created here")

    def test_execute_example_launch_file_not_found(self) -> None:
        with self.assertRaises(FileNotFoundError):
            execute_example_launch("examples/non_existent_dir/launch.py")

    def test_port_forward_grpc_service_existing_endpoint(self) -> None:
        existing_ep = "127.0.0.1:12345"
        with patch.dict(os.environ, {"XMANAGER_ENDPOINT": existing_ep}):
            with port_forward_grpc_service() as ep:
                self.assertEqual(ep, existing_ep)

    @patch("subprocess.run")
    @patch("pathlib.Path.exists", return_value=True)
    @patch("test.e2e.examples.example_runner.fetch_dex_oidc_token")
    def test_execute_example_launch_success(
        self,
        mock_gen_token: MagicMock,
        mock_exists: MagicMock,
        mock_subprocess_run: MagicMock,
    ) -> None:
        mock_gen_token.return_value = "mock.jwt.token"
        mock_proc = MagicMock()
        mock_proc.returncode = 0
        mock_proc.stdout = (
            "Created Work Unit successfully: experiments/100/workUnits/2\n"
            "Created Chart Artifact successfully: experiments/100/workUnits/2/artifacts/555\n"
        )
        mock_proc.stderr = ""
        mock_subprocess_run.return_value = mock_proc

        with patch("test.e2e.examples.example_runner.port_forward_grpc_service") as mock_pf:
            mock_pf.return_value.__enter__.return_value = "127.0.0.1:50051"
            res = execute_example_launch(
                launch_script="examples/chart_artifact_job/launch.py",
                user_email=ADMIN_USER,
            )
            self.assertEqual(res.xid, "100")
            self.assertEqual(res.wid, "2")
            self.assertEqual(res.artifact_name, "experiments/100/workUnits/2/artifacts/555")
            mock_subprocess_run.assert_called_once()

    @patch("subprocess.run")
    @patch("pathlib.Path.exists", return_value=True)
    @patch("test.e2e.examples.example_runner.fetch_dex_oidc_token")
    def test_execute_example_launch_recycles_existing_auth_token(
        self,
        mock_gen_token: MagicMock,
        mock_exists: MagicMock,
        mock_subprocess_run: MagicMock,
    ) -> None:
        mock_proc = MagicMock()
        mock_proc.returncode = 0
        mock_proc.stdout = "Created Work Unit successfully: experiments/100/workUnits/2\n"
        mock_proc.stderr = ""
        mock_subprocess_run.return_value = mock_proc

        with patch.dict(os.environ, {"XMC_AUTH_TOKEN": "pre-existing-token"}):
            with patch("test.e2e.examples.example_runner.port_forward_grpc_service") as mock_pf:
                mock_pf.return_value.__enter__.return_value = "127.0.0.1:50051"
                res = execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
                    user_email=ADMIN_USER,
                )
                self.assertEqual(res.xid, "100")
                mock_gen_token.assert_not_called()
                called_env = mock_subprocess_run.call_args[1]["env"]
                self.assertEqual(called_env["XMC_AUTH_TOKEN"], "pre-existing-token")

    @patch("subprocess.run")
    @patch("pathlib.Path.exists", return_value=True)
    @patch("test.e2e.examples.example_runner.fetch_dex_oidc_token")
    def test_execute_example_launch_subprocess_error(
        self,
        mock_gen_token: MagicMock,
        mock_exists: MagicMock,
        mock_subprocess_run: MagicMock,
    ) -> None:
        mock_gen_token.return_value = "mock.jwt.token"
        mock_proc = MagicMock()
        mock_proc.returncode = 1
        mock_proc.stdout = ""
        mock_proc.stderr = "Traceback (most recent call last): SomeError"
        mock_subprocess_run.return_value = mock_proc

        with patch("test.e2e.examples.example_runner.port_forward_grpc_service") as mock_pf:
            mock_pf.return_value.__enter__.return_value = "127.0.0.1:50051"
            with self.assertRaises(RuntimeError) as ctx:
                execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
                    user_email=ADMIN_USER,
                )
            self.assertIn("exited with code 1", str(ctx.exception))

    @patch("subprocess.run")
    @patch("pathlib.Path.exists", return_value=True)
    @patch("test.e2e.examples.example_runner.fetch_dex_oidc_token")
    def test_execute_example_launch_timeout(
        self,
        mock_gen_token: MagicMock,
        mock_exists: MagicMock,
        mock_subprocess_run: MagicMock,
    ) -> None:
        mock_gen_token.return_value = "mock.jwt.token"
        mock_subprocess_run.side_effect = subprocess.TimeoutExpired(
            cmd=["python3", "launch.py"],
            timeout=15.0,
            output="partial stdout stream\n",
            stderr="partial stderr stream\n",
        )

        with patch("test.e2e.examples.example_runner.port_forward_grpc_service") as mock_pf:
            mock_pf.return_value.__enter__.return_value = "127.0.0.1:50051"
            with self.assertRaises(RuntimeError) as ctx:
                execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
                    user_email=ADMIN_USER,
                    timeout_sec=15.0,
                )
            self.assertIn("Example launch timed out after 15.0s:", str(ctx.exception))
            self.assertIn("partial stdout stream", str(ctx.exception))
            self.assertIn("partial stderr stream", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
