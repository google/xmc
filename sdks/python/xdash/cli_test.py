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
from xdash.presets import create_vcc_comparison_dashboard


class CliTest(unittest.TestCase):
    """Tests for xdash CLI command parsing and execution."""

    def setUp(self) -> None:
        self.mock_client = MagicMock()
        # A real CreateChart echoes the chart back with its resolved resource name, and
        # the CLI derives the dashboard's chart IDs from those names.
        self.mock_client.create_charts.side_effect = lambda charts: list(charts)

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

    def test_create_preset(self) -> None:
        self.mock_client.create_dashboard.return_value = create_vcc_comparison_dashboard()

        ret, out, _ = self._run(
            ["create-preset", "--dashboard-id", "test_dash", "--title", "Test Title"]
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()
        args, kwargs = self.mock_client.create_dashboard.call_args
        self.assertEqual(args[0].name, "dashboards/test_dash")
        self.assertEqual(args[0].title, "Test Title")
        self.assertEqual(list(args[0].chart_ids), ["baseline", "tuned_v1", "tuned_v2"])
        self.assertEqual(kwargs["dashboard_id"], "test_dash")
        self.assertIn("3 charts", out)
        # Charts must be created before the dashboard that references them.
        self.mock_client.create_charts.assert_called_once()

    def test_create_preset_defaults(self) -> None:
        self.mock_client.create_dashboard.return_value = create_vcc_comparison_dashboard()

        ret, _, _ = self._run(["create-preset"])
        self.assertEqual(ret, 0)
        built = self.mock_client.create_dashboard.call_args[0][0]
        self.assertEqual(built.name, "dashboards/vcc_comparison")
        self.assertEqual(built.title, "VCC baseline tune comparison")

    def test_create_dashboard_with_xids(self) -> None:
        self.mock_client.create_dashboard.return_value = messages_pb2.Dashboard(
            name="dashboards/d1", title="Dash 1"
        )

        ret, _, _ = self._run(
            [
                "create-dashboard",
                "--title",
                "Dash 1",
                "--dashboard-id",
                "d1",
                "--xids",
                "234911122, 234911123, ",
            ]
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()
        built = self.mock_client.create_dashboard.call_args[0][0]
        # Whitespace trimmed and the trailing empty element dropped, not turned into a chart.
        self.assertEqual(list(built.chart_ids), ["exp_234911122", "exp_234911123"])
        created_charts = self.mock_client.create_charts.call_args[0][0]
        self.assertEqual([chart.xid for chart in created_charts], [234911122, 234911123])
        self.assertEqual(
            [chart.name for chart in created_charts],
            [
                "experiments/234911122/workUnits/1/charts/exp_234911122",
                "experiments/234911123/workUnits/1/charts/exp_234911123",
            ],
        )

    def test_create_dashboard_rejects_invalid_xids(self) -> None:
        for bad in ("0", "-5", "not-a-number"):
            with self.subTest(xids=bad):
                ret, _, err = self._run(["create-dashboard", "--title", "Dash 1", "--xids", bad])
                self.assertEqual(ret, 1)
                self.assertIn("Error:", err)
        self.mock_client.create_dashboard.assert_not_called()

    def test_create_dashboard(self) -> None:
        self.mock_client.create_dashboard.return_value = messages_pb2.Dashboard(
            name="dashboards/d1", title="Dash 1"
        )

        ret, out, _ = self._run(
            [
                "create-dashboard",
                "--title",
                "Dash 1",
                "--dashboard-id",
                "d1",
                "--description",
                "First dashboard",
            ]
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()
        built = self.mock_client.create_dashboard.call_args[0][0]
        self.assertEqual(built.name, "dashboards/d1")
        self.assertEqual(built.title, "Dash 1")
        self.assertEqual(built.description, "First dashboard")
        self.assertIn("dashboards/d1", out)

    def test_create_dashboard_without_an_id_slugifies_the_title(self) -> None:
        self.mock_client.create_dashboard.return_value = messages_pb2.Dashboard(
            name="dashboards/dash_1", title="Dash 1"
        )

        ret, _, _ = self._run(["create-dashboard", "--title", "Dash 1"])
        self.assertEqual(ret, 0)
        args, kwargs = self.mock_client.create_dashboard.call_args
        # No --dashboard-id, so the builder derives the resource name from the title and
        # the explicit id is left to the client to pull back out of it.
        self.assertEqual(args[0].name, "dashboards/dash_1")
        self.assertIsNone(kwargs["dashboard_id"])

    def test_create_chart(self) -> None:
        self.mock_client.create_chart.return_value = messages_pb2.Chart(
            name="experiments/1001/workUnits/1/charts/c1", title="Chart 1"
        )

        ret, out, _ = self._run(
            [
                "create-chart",
                "--parent",
                "experiments/1001/workUnits/1",
                "--xid",
                "1001",
                "--wid",
                "1",
                "--title",
                "Chart 1",
                "--metrics",
                "val/loss",
            ]
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_chart.assert_called_once()
        kwargs = self.mock_client.create_chart.call_args[1]
        self.assertEqual(kwargs["parent"], "experiments/1001/workUnits/1")
        built = kwargs["chart"]
        self.assertEqual(built.title, "Chart 1")
        self.assertEqual((built.xid, built.wid), (1001, 1))
        self.assertIn("experiments/1001/workUnits/1/charts/c1", out)

    def test_create_chart_with_multiple_metrics(self) -> None:
        self.mock_client.create_chart.return_value = messages_pb2.Chart(name="c")

        ret, _, _ = self._run(
            [
                "create-chart",
                "--parent",
                "experiments/1001/workUnits/1",
                "--title",
                "Chart 1",
                "--metrics",
                " val/loss , train/accuracy ,",
            ]
        )
        self.assertEqual(ret, 0)
        built = self.mock_client.create_chart.call_args[1]["chart"]
        # A KPI and a scatter plot per metric; names trimmed, trailing empty element dropped.
        self.assertEqual(len(built.plots), 4)
        self.assertEqual(
            [p.plot_id for p in built.plots],
            ["kpi_0", "scatter_0", "kpi_1", "scatter_1"],
        )
        self.assertEqual(
            [list(p.data.metrics) for p in built.plots],
            [["val/loss"], ["val/loss"], ["train/accuracy"], ["train/accuracy"]],
        )

    def test_create_chart_derives_ids_from_parent(self) -> None:
        self.mock_client.create_chart.return_value = messages_pb2.Chart(
            name="experiments/1001/workUnits/3/charts/c1", title="Chart 1"
        )

        ret, _, _ = self._run(
            [
                "create-chart",
                "--parent",
                "experiments/1001/workUnits/3",
                "--title",
                "Chart 1",
                "--metrics",
                "val/loss",
            ]
        )
        self.assertEqual(ret, 0)
        built = self.mock_client.create_chart.call_args[1]["chart"]
        self.assertEqual((built.xid, built.wid), (1001, 3))

    def test_create_chart_rejects_ids_conflicting_with_parent(self) -> None:
        for flag, value in (("--xid", "2002"), ("--wid", "7")):
            with self.subTest(flag=flag):
                ret, _, err = self._run(
                    [
                        "create-chart",
                        "--parent",
                        "experiments/1001/workUnits/1",
                        flag,
                        value,
                        "--title",
                        "Chart 1",
                        "--metrics",
                        "val/loss",
                    ]
                )
                self.assertEqual(ret, 1)
                self.assertIn("Error:", err)
        self.mock_client.create_chart.assert_not_called()

    def test_create_chart_rejects_non_work_unit_parent(self) -> None:
        ret, _, err = self._run(
            [
                "create-chart",
                "--parent",
                "dashboards/d1",
                "--title",
                "Chart 1",
                "--metrics",
                "val/loss",
            ]
        )
        self.assertEqual(ret, 1)
        self.assertIn("experiments/{xid}/workUnits/{wid}", err)
        self.mock_client.create_chart.assert_not_called()

    def test_create_chart_rejects_empty_metrics(self) -> None:
        ret, _, err = self._run(
            [
                "create-chart",
                "--parent",
                "experiments/1001/workUnits/1",
                "--xid",
                "1001",
                "--title",
                "Chart 1",
                "--metrics",
                " , ",
            ]
        )
        self.assertEqual(ret, 1)
        self.assertIn("Error:", err)
        self.mock_client.create_chart.assert_not_called()

    def test_get_dashboard(self) -> None:
        self.mock_client.get_dashboard.return_value = messages_pb2.Dashboard(
            name="dashboards/d1",
            title="D1",
            description="First dashboard",
            chart_ids=["c1"],
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

    def test_get_data_renders_every_content_kind(self) -> None:
        plot_data = messages_pb2.ChartData.PlotData
        self.mock_client.get_chart_data.return_value = messages_pb2.ChartData(
            name="experiments/1001/charts/c1",
            xid=1001,
            wid=1,
            plots_data=[
                plot_data(
                    plot_id="kpi",
                    summary_data=messages_pb2.ChartData.SummaryBoxValue(latest_value=0.1234),
                ),
                plot_data(
                    plot_id="curve",
                    scatter_series=messages_pb2.ChartData.SeriesData(
                        points=[
                            messages_pb2.ChartData.SeriesPoint(),
                            messages_pb2.ChartData.SeriesPoint(),
                            messages_pb2.ChartData.SeriesPoint(),
                        ]
                    ),
                ),
                plot_data(
                    plot_id="hist",
                    bar_data=messages_pb2.ChartData.BarData(
                        buckets=[messages_pb2.ChartData.BarBucket(category_or_bin="a")]
                    ),
                ),
                # No content member set: the oneof dispatch has to fall through.
                plot_data(plot_id="blank"),
            ],
        )

        ret, out, _ = self._run(["get-data", "experiments/1001/charts/c1"])
        self.assertEqual(ret, 0)
        self.mock_client.get_chart_data.assert_called_once_with("experiments/1001/charts/c1")
        self.assertIn("ChartData for experiments/1001/charts/c1 (xid=1001, wid=1):", out)
        self.assertIn("Total plots data returned: 4", out)
        self.assertIn("  - Plot [kpi]: Summary value = 0.1234", out)
        self.assertIn("  - Plot [curve]: Scatter series = 3 points", out)
        self.assertIn("  - Plot [hist]: Bar buckets = 1", out)
        self.assertIn("  - Plot [blank]: Empty content", out)

    def test_get_data_with_no_plots(self) -> None:
        self.mock_client.get_chart_data.return_value = messages_pb2.ChartData(
            name="experiments/1001/charts/c1"
        )
        ret, out, _ = self._run(["get-data", "experiments/1001/charts/c1"])
        self.assertEqual(ret, 0)
        self.assertIn("Total plots data returned: 0", out)

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
