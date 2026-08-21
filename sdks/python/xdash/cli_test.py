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
"""Unit tests for xdash command-line interface."""

import contextlib
import io
import unittest
from unittest.mock import MagicMock, patch

from xdash._protos import messages_pb2
from xdash.cli import run_cli
from xdash.client import XDashConnectionError, XDashRPCError


class CliTest(unittest.TestCase):
    """Tests for xdash CLI command parsing and execution."""

    def setUp(self) -> None:
        self.mock_client = MagicMock()

    def _run(self, argv: list[str]) -> tuple[int, str, str]:
        """Runs the CLI with a mock client, capturing its exit code, stdout and stderr."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            ret = run_cli(argv, client=self.mock_client)
        return ret, out.getvalue(), err.getvalue()

    def test_no_command_returns_help_error(self) -> None:
        ret, out, _ = self._run([])
        self.assertEqual(ret, 1)
        self.assertIn("usage: xdash", out)

    def test_get_dashboard(self) -> None:
        self.mock_client.get_dashboard.return_value = messages_pb2.Dashboard(
            name="dashboards/d1",
            title="D1",
            description="First dashboard",
            charts=[messages_pb2.Chart(name="dashboards/d1/charts/c1")],
        )

        ret, out, _ = self._run(["get", "dashboards/d1"])
        self.assertEqual(ret, 0)
        self.mock_client.get_dashboard.assert_called_once_with("dashboards/d1")
        self.assertIn("Dashboard: dashboards/d1", out)
        self.assertIn("Title:       D1", out)
        self.assertIn("Description: First dashboard", out)
        self.assertIn("Charts:      1", out)

    def test_get_chart(self) -> None:
        self.mock_client.get_chart.return_value = messages_pb2.Chart(
            name="experiments/1001/charts/c1",
            title="C1",
            xid=1001,
            wid=7,
            plots=[messages_pb2.PlotSpec(plot_id="p1"), messages_pb2.PlotSpec(plot_id="p2")],
        )

        ret, out, _ = self._run(["get", "experiments/1001/charts/c1"])
        self.assertEqual(ret, 0)
        self.mock_client.get_chart.assert_called_once_with("experiments/1001/charts/c1")
        self.assertIn("Chart:       experiments/1001/charts/c1", out)
        self.assertIn("Experiment:  1001 (wid: 7)", out)
        self.assertIn("Plots:       2", out)

    def test_get_routes_dashboard_sub_chart_to_get_chart(self) -> None:
        self.mock_client.get_chart.return_value = messages_pb2.Chart(
            name="dashboards/d1/charts/c1", title="Sub Chart"
        )

        ret, _, _ = self._run(["get", "dashboards/d1/charts/c1"])
        self.assertEqual(ret, 0)
        self.mock_client.get_chart.assert_called_once_with("dashboards/d1/charts/c1")
        self.mock_client.get_dashboard.assert_not_called()

    def test_get_strips_surrounding_whitespace(self) -> None:
        self.mock_client.get_dashboard.return_value = messages_pb2.Dashboard(name="dashboards/d1")
        ret, _, _ = self._run(["get", "  dashboards/d1  "])
        self.assertEqual(ret, 0)
        self.mock_client.get_dashboard.assert_called_once_with("dashboards/d1")

    def test_get_rejects_blank_name_without_calling_the_service(self) -> None:
        ret, _, err = self._run(["get", "   "])
        self.assertEqual(ret, 1)
        self.assertIn("Resource name cannot be blank", err)
        self.mock_client.get_dashboard.assert_not_called()
        self.mock_client.get_chart.assert_not_called()

    def test_get_rejects_unrecognised_resource_prefix(self) -> None:
        ret, _, err = self._run(["get", "projects/p1/charts/c1"])
        self.assertEqual(ret, 1)
        self.assertIn("Unrecognised resource name", err)
        self.mock_client.get_chart.assert_not_called()

    def test_get_reports_rpc_failure(self) -> None:
        self.mock_client.get_dashboard.side_effect = XDashRPCError(
            "GetDashboard failed: Resource not found", code="NOT_FOUND"
        )
        ret, _, err = self._run(["get", "dashboards/d1"])
        self.assertEqual(ret, 1)
        self.assertIn("Error: GetDashboard failed: Resource not found", err)

    def test_list(self) -> None:
        self.mock_client.list_dashboards.return_value = [
            messages_pb2.Dashboard(name="dashboards/1", title="D1"),
            messages_pb2.Dashboard(name="dashboards/2", title="D2"),
        ]
        ret, out, _ = self._run(["list"])
        self.assertEqual(ret, 0)
        self.mock_client.list_dashboards.assert_called_once_with()
        self.assertIn("Found 2 dashboards:", out)
        self.assertIn("  - [dashboards/1] D1", out)
        self.assertIn("  - [dashboards/2] D2", out)

    def test_list_with_no_dashboards(self) -> None:
        self.mock_client.list_dashboards.return_value = []
        ret, out, _ = self._run(["list"])
        self.assertEqual(ret, 0)
        self.assertIn("Found 0 dashboards:", out)


class CliClientLifecycleTest(unittest.TestCase):
    """Tests that run_cli owns and releases the client it creates itself."""

    def setUp(self) -> None:
        """Captures CLI output; these tests assert on lifecycle, not on what is printed."""
        stack = contextlib.ExitStack()
        self.stdout = stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
        self.stderr = stack.enter_context(contextlib.redirect_stderr(io.StringIO()))
        self.addCleanup(stack.close)

    def test_created_client_is_closed_on_success(self) -> None:
        with patch("xdash.cli.DashboardClient") as mock_cls:
            mock_cls.return_value.list_dashboards.return_value = []
            ret = run_cli(["list"])
        self.assertEqual(ret, 0)
        mock_cls.assert_called_once_with(endpoint=None, secure=None)
        mock_cls.return_value.close.assert_called_once()

    def test_created_client_is_closed_on_failure(self) -> None:
        with patch("xdash.cli.DashboardClient") as mock_cls:
            mock_cls.return_value.get_dashboard.side_effect = ValueError("boom")
            ret = run_cli(["get", "dashboards/d1"])
        self.assertEqual(ret, 1)
        mock_cls.return_value.close.assert_called_once()

    def test_injected_client_is_not_closed(self) -> None:
        mock_client = MagicMock()
        mock_client.list_dashboards.return_value = []
        run_cli(["list"], client=mock_client)
        mock_client.close.assert_not_called()

    def test_connection_failure_is_reported_not_raised(self) -> None:
        # Constructing the client happens inside run_cli's try, so a channel that will
        # not open prints "Error: ..." rather than dumping a traceback on the user.
        with patch("xdash.cli.DashboardClient") as mock_cls:
            mock_cls.side_effect = XDashConnectionError("Failed to create gRPC channel")
            ret = run_cli(["list"])
        self.assertEqual(ret, 1)
        self.assertIn("Error: Failed to create gRPC channel", self.stderr.getvalue())
        mock_cls.return_value.close.assert_not_called()

    def test_insecure_flag_forces_plaintext_channel(self) -> None:
        with patch("xdash.cli.DashboardClient") as mock_cls:
            mock_cls.return_value.list_dashboards.return_value = []
            run_cli(["--insecure", "--endpoint", "dashboards.example.com:443", "list"])
        mock_cls.assert_called_once_with(endpoint="dashboards.example.com:443", secure=False)


if __name__ == "__main__":
    unittest.main()
