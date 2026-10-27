# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for cluster_inspector."""

import subprocess
import unittest
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.migrations.components import cluster_inspector


class TestClusterInspector(unittest.TestCase):
    """Tests for read-only Helm and Kubernetes cluster inspection queries."""

    @patch("tools.migrations.components.cluster_inspector.run_command")
    def test_fetch_umbrella_values_success(self, mock_run: MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout='{"ingestionGatewayDeployment": {"enabled": true}}',
            stderr="",
        )
        got = cluster_inspector.fetch_umbrella_values("xmc", "tenant-a")
        self.assertEqual(got, {"ingestionGatewayDeployment": {"enabled": True}})
        self.assertEqual(
            mock_run.call_args[0][0],
            ["helm", "get", "values", "xmc", "-n", "tenant-a", "-a", "-o", "json"],
        )

    @patch(
        "tools.migrations.components.cluster_inspector.run_command",
        side_effect=subprocess.CalledProcessError(1, ["helm"], stderr="Error: release: not found"),
    )
    def test_fetch_umbrella_values_failure_raises_handled_error(self, _mock_run: MagicMock) -> None:
        with self.assertRaises(HandledError) as ctx:
            cluster_inspector.fetch_umbrella_values("xmc", "xmc")
        self.assertIn("not found", ctx.exception.message)

    @patch("tools.migrations.components.cluster_inspector.run_command")
    def test_fetch_live_resource_optional_and_invalid_json(self, mock_run: MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=1,
            stdout="",
            stderr='Error from server (NotFound): hpa "x" not found',
        )
        self.assertIsNone(
            cluster_inspector.fetch_live_resource(
                "horizontalpodautoscaler", "x", "xmc", required=False
            )
        )
        with self.assertRaises(HandledError):
            cluster_inspector.fetch_live_resource(
                "horizontalpodautoscaler", "x", "xmc", required=True
            )

        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="not-json", stderr=""
        )
        with self.assertRaises(HandledError):
            cluster_inspector.fetch_live_resource("deployment", "x", "xmc", required=True)

        mock_run.side_effect = FileNotFoundError("kubectl")
        with self.assertRaises(HandledError):
            cluster_inspector.fetch_live_resource("deployment", "x", "xmc", required=True)

    @patch("tools.migrations.components.cluster_inspector.run_command")
    def test_validate_namespace_and_annotate_resource(self, mock_run: MagicMock) -> None:
        cluster_inspector.validate_dns1123_namespace("tenant-a")
        with self.assertRaises(HandledError):
            cluster_inspector.validate_dns1123_namespace("BAD_NS")

        mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
        cluster_inspector.annotate_resource("service", "s1", "tenant-a", ["k=v"])
        mock_run.side_effect = FileNotFoundError("kubectl")
        with self.assertRaises(HandledError):
            cluster_inspector.annotate_resource("service", "s1", "tenant-a", ["k=v"])

    @patch("tools.migrations.components.cluster_inspector.run_command")
    def test_verify_secret_exists_and_helm_release_exists(self, mock_run: MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout='{"kind": "Secret"}', stderr=""
        )
        cluster_inspector.verify_secret_exists("ch-sec", "xmc", field_label="ClickHouse")
        self.assertTrue(cluster_inspector.helm_release_exists("xmc-cmp-ingestion-gateway", "xmc"))

        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=1, stdout="", stderr="Error: release: not found"
        )
        self.assertFalse(cluster_inspector.helm_release_exists("xmc-cmp-ingestion-gateway", "xmc"))
        with self.assertRaises(HandledError):
            cluster_inspector.verify_secret_exists("ch-sec", "xmc", field_label="ClickHouse")

        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=1, stdout="", stderr="Error: Kubernetes cluster unreachable"
        )
        with self.assertRaises(HandledError):
            cluster_inspector.helm_release_exists("xmc-cmp-ingestion-gateway", "xmc")


if __name__ == "__main__":
    unittest.main()
