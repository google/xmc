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

"""Unit tests for ClusterDiagnostics."""

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.dev_env.cluster_diagnostics import ClusterDiagnostics


class TestClusterDiagnostics(unittest.TestCase):
    """Test suite for ClusterDiagnostics telemetry collection."""

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.tmp_dir.name)
        self.diagnostics = ClusterDiagnostics(
            kube_context="kind-test-cluster",
            output_dir=self.temp_path,
        )

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    @patch("tools.dev_env.cluster_diagnostics.shutil.which", return_value="/usr/bin/kubectl")
    @patch("tools.dev_env.cluster_diagnostics.run_command")
    def test_capture_diagnostics_success(self, mock_run: MagicMock, mock_which: MagicMock) -> None:
        def side_effect(
            cmd: list[str], *args: object, **kwargs: object
        ) -> subprocess.CompletedProcess[str]:
            cmd_str = " ".join(cmd)
            if "get nodes" in cmd_str:
                return subprocess.CompletedProcess(cmd, 0, stdout="node1 Ready", stderr="")
            if "get pods" in cmd_str and "jsonpath" in cmd_str:
                return subprocess.CompletedProcess(cmd, 0, stdout="pod-1 pod-2", stderr="")
            if "logs" in cmd_str:
                return subprocess.CompletedProcess(cmd, 0, stdout="Container log line", stderr="")
            if "get events" in cmd_str:
                return subprocess.CompletedProcess(
                    cmd, 0, stdout="Event: Normal Scheduled", stderr=""
                )
            return subprocess.CompletedProcess(cmd, 0, stdout="NAME STATUS", stderr="")

        mock_run.side_effect = side_effect
        diag_path = self.diagnostics.capture_diagnostics()

        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("=== PODS (namespace: xmc) ===", content)
        self.assertIn("=== POD DESCRIPTIONS (namespace: xmc) ===", content)
        self.assertIn("=== EVENTS (namespace: xmc) ===", content)
        self.assertIn("--- Logs for pod: pod-1 (ns: xmc) ---", content)
        self.assertIn("Container log line", content)

    @patch("tools.dev_env.cluster_diagnostics.shutil.which")
    @patch("tools.dev_env.cluster_diagnostics.run_command")
    def test_capture_diagnostics_no_kubectl(
        self, mock_run: MagicMock, mock_which: MagicMock
    ) -> None:
        mock_which.return_value = None
        diag_path = self.diagnostics.capture_diagnostics()

        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("kubectl CLI not found", content)
        mock_run.assert_not_called()

    @patch("tools.dev_env.cluster_diagnostics.shutil.which", return_value="/usr/bin/kubectl")
    @patch("tools.dev_env.cluster_diagnostics.run_command")
    def test_capture_diagnostics_unreachable_cluster(
        self, mock_run: MagicMock, mock_which: MagicMock
    ) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            ["kubectl", "get", "nodes"], 1, stdout="", stderr="connection refused"
        )
        diag_path = self.diagnostics.capture_diagnostics()
        self.assertTrue(diag_path.is_file())
        content = diag_path.read_text(encoding="utf-8")
        self.assertIn("Cluster unreachable: connection refused", content)
        self.assertEqual(mock_run.call_count, 1)
