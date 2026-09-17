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
    ExampleLaunchResult,
    execute_example_launch,
    is_port_open,
    parse_launch_output,
    resolve_grpc_endpoint,
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

    def test_parse_launch_output_chart_artifact_job(self) -> None:
        sample_output = """
[1/4] Created Experiment successfully: experiments/234911125 (XID: 234911125)
[2/4] Created WorkUnit successfully: experiments/234911125/workUnits/1 (WID: 1)
--> Publishing Dashboard 'Chart Artifact Experiment 234911125' to HTTP REST https://xmcv2.example.com/v1/dashboards...
--> Created Dashboard successfully: dashboards/chart_artifact_x234911125_w1 (HTTP 200)
[4/4] Attaching artifacts to the WorkUnit...
--> Created Chart artifact: experiments/234911125/workUnits/1/artifacts/1
--> Created Dashboard link artifact: experiments/234911125/workUnits/1/artifacts/2
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "234911125")
        self.assertEqual(result.wid, "1")
        self.assertEqual(result.artifact_name, "experiments/234911125/workUnits/1/artifacts/1")
        self.assertEqual(result.experiment_name, "experiments/234911125")
        self.assertEqual(result.canonical_work_unit_name, "experiments/234911125/workUnits/1")

    def test_parse_launch_output_metrics_dashboard_job(self) -> None:
        sample_output = """
[1/3] Created Experiment: experiments/45678 (XID: 45678)
[2/3] Created WorkUnit: experiments/45678/workUnits/2 (WID: 2)
[3/3] Creating Charts and Dashboard...
--> Created Dashboard successfully: dashboards/exp_45678_overview
==================================================
  🎉 Launch completed
  Experiment XID : 45678
  WorkUnit ID    : 2
  Charts populate as the worker streams metrics.
==================================================
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "45678")
        self.assertEqual(result.wid, "2")
        self.assertIsNone(result.artifact_name)
        self.assertEqual(result.experiment_name, "experiments/45678")
        self.assertEqual(result.canonical_work_unit_name, "experiments/45678/workUnits/2")

    def test_parse_launch_output_managed_xprof_artifact_job(self) -> None:
        sample_output = """
[1/3] Created Experiment: experiments/98765 (XID: 98765)
✅ Created Work Unit: experiments/98765/workUnits/3
📎 Attached ManagedXprof artifact: experiments/98765/workUnits/3/artifacts/12
🎉 Success! View Experiment Dashboard:
👉 http://localhost:8080/experiments/98765
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "98765")
        self.assertEqual(result.wid, "3")
        self.assertEqual(result.artifact_name, "experiments/98765/workUnits/3/artifacts/12")
        self.assertEqual(result.experiment_name, "experiments/98765")
        self.assertEqual(result.canonical_work_unit_name, "experiments/98765/workUnits/3")

    def test_parse_launch_output_simple_job(self) -> None:
        sample_output = """
  [+] WorkUnit #0 created: ID=1 (Worker=simple-job-0)
  [+] WorkUnit #1 created: ID=2 (Worker=simple-job-1)

✅ All WorkUnits created successfully!
   Experiment XID: 54321
   WorkUnit IDs  : 1, 2

📊 Monitor in XMC Web UI:
   http://localhost:8080/experiments/54321
=================================================================
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "54321")
        self.assertEqual(result.wid, "1")
        self.assertIsNone(result.artifact_name)
        self.assertEqual(result.experiment_name, "experiments/54321")
        self.assertEqual(result.canonical_work_unit_name, "experiments/54321/workUnits/1")

    def test_parse_launch_output_clickhouse_metrics_job(self) -> None:
        sample_output = """
[1/3] Created Experiment successfully: experiments/112233 (XID: 112233)
[2/3] Created WorkUnit successfully: experiments/112233/workUnits/4 (WID: 4)
--> Created Dashboard successfully: dashboards/exp_112233_training_overview (HTTP 200)
"""
        result = parse_launch_output(sample_output)
        self.assertEqual(result.xid, "112233")
        self.assertEqual(result.wid, "4")
        self.assertIsNone(result.artifact_name)
        self.assertEqual(result.experiment_name, "experiments/112233")
        self.assertEqual(result.canonical_work_unit_name, "experiments/112233/workUnits/4")

    def test_parse_launch_output_fails_on_empty(self) -> None:
        with self.assertRaises(RuntimeError):
            parse_launch_output("no experiment created here")

    def test_execute_example_launch_file_not_found(self) -> None:
        with self.assertRaises(FileNotFoundError):
            execute_example_launch("examples/non_existent_dir/launch.py")

    def test_resolve_grpc_endpoint_configured_env(self) -> None:
        configured_ep = "127.0.0.1:12345"
        with patch.dict(os.environ, {"XMANAGER_ENDPOINT": configured_ep}):
            with resolve_grpc_endpoint() as ep:
                self.assertEqual(ep, configured_ep)

    def test_resolve_grpc_endpoint_open_port(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            with patch("test.e2e.examples.example_runner.is_port_open", return_value=True):
                with resolve_grpc_endpoint(remote_port=50051) as ep:
                    self.assertEqual(ep, "127.0.0.1:50051")

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

        with patch("test.e2e.examples.example_runner.resolve_grpc_endpoint") as mock_resolve:
            mock_resolve.return_value.__enter__.return_value = "127.0.0.1:50051"
            res = execute_example_launch(
                launch_script="examples/chart_artifact_job/launch.py",
                user_email="admin@xmc.local",
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
            with patch("test.e2e.examples.example_runner.resolve_grpc_endpoint") as mock_resolve:
                mock_resolve.return_value.__enter__.return_value = "127.0.0.1:50051"
                res = execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
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

        with patch("test.e2e.examples.example_runner.resolve_grpc_endpoint") as mock_resolve:
            mock_resolve.return_value.__enter__.return_value = "127.0.0.1:50051"
            with self.assertRaises(RuntimeError) as ctx:
                execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
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

        with patch("test.e2e.examples.example_runner.resolve_grpc_endpoint") as mock_resolve:
            mock_resolve.return_value.__enter__.return_value = "127.0.0.1:50051"
            with self.assertRaises(RuntimeError) as ctx:
                execute_example_launch(
                    launch_script="examples/chart_artifact_job/launch.py",
                    timeout_sec=15.0,
                )
            self.assertIn("Example launch timed out after 15.0s:", str(ctx.exception))
            self.assertIn("partial stdout stream", str(ctx.exception))
            self.assertIn("partial stderr stream", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
