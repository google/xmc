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

import unittest
from unittest.mock import MagicMock, patch

from xdash._protos import messages_pb2
from xdash.cli import run_cli


class CliTest(unittest.TestCase):
    """Tests for xdash CLI command parsing and execution."""

    def setUp(self) -> None:
        self.mock_client = MagicMock()

    def test_no_command_returns_help_error(self) -> None:
        ret = run_cli([], client=self.mock_client)
        self.assertEqual(ret, 1)

    def test_create_dashboard(self) -> None:
        dash = messages_pb2.Dashboard(name="dashboards/d1", title="Dash 1")
        self.mock_client.create_dashboard.return_value = dash

        ret = run_cli(
            [
                "create-dashboard",
                "--title",
                "Dash 1",
                "--dashboard-id",
                "d1",
                "--description",
                "First dashboard",
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()
        built = self.mock_client.create_dashboard.call_args[0][0]
        self.assertEqual(built.name, "dashboards/d1")
        self.assertEqual(built.description, "First dashboard")

    def test_create_chart(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/workUnits/1/charts/c1", title="Chart 1")
        self.mock_client.create_chart.return_value = chart

        ret = run_cli(
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
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_chart.assert_called_once()

    def test_create_chart_derives_ids_from_parent(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/workUnits/3/charts/c1", title="Chart 1")
        self.mock_client.create_chart.return_value = chart

        ret = run_cli(
            [
                "create-chart",
                "--parent",
                "experiments/1001/workUnits/3",
                "--title",
                "Chart 1",
                "--metrics",
                "val/loss",
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 0)
        built = self.mock_client.create_chart.call_args[1]["chart"]
        self.assertEqual((built.xid, built.wid), (1001, 3))

    def test_create_chart_rejects_ids_conflicting_with_parent(self) -> None:
        for flag, value in (("--xid", "2002"), ("--wid", "7")):
            with self.subTest(flag=flag):
                ret = run_cli(
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
                    ],
                    client=self.mock_client,
                )
                self.assertEqual(ret, 1)
        self.mock_client.create_chart.assert_not_called()

    def test_create_chart_rejects_non_work_unit_parent(self) -> None:
        ret = run_cli(
            [
                "create-chart",
                "--parent",
                "dashboards/d1",
                "--title",
                "Chart 1",
                "--metrics",
                "val/loss",
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 1)
        self.mock_client.create_chart.assert_not_called()

    def test_create_chart_rejects_empty_metrics(self) -> None:
        ret = run_cli(
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
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 1)
        self.mock_client.create_chart.assert_not_called()

    def test_get_dashboard_and_chart(self) -> None:
        dash = messages_pb2.Dashboard(name="dashboards/d1", title="D1")
        self.mock_client.get_dashboard.return_value = dash

        ret_dash = run_cli(["get", "dashboards/d1"], client=self.mock_client)
        self.assertEqual(ret_dash, 0)
        self.mock_client.get_dashboard.assert_called_once_with("dashboards/d1")

        chart = messages_pb2.Chart(name="experiments/1001/charts/c1", title="C1", xid=1001, wid=1)
        self.mock_client.get_chart.return_value = chart

        ret_chart = run_cli(["get", "experiments/1001/charts/c1"], client=self.mock_client)
        self.assertEqual(ret_chart, 0)
        self.mock_client.get_chart.assert_called_once_with("experiments/1001/charts/c1")

    def test_get_routes_dashboard_sub_chart_to_get_chart(self) -> None:
        chart = messages_pb2.Chart(name="dashboards/d1/charts/c1", title="Sub Chart")
        self.mock_client.get_chart.return_value = chart

        ret = run_cli(["get", "dashboards/d1/charts/c1"], client=self.mock_client)
        self.assertEqual(ret, 0)
        self.mock_client.get_chart.assert_called_once_with("dashboards/d1/charts/c1")
        self.mock_client.get_dashboard.assert_not_called()

    def test_get_reports_rpc_failure(self) -> None:
        self.mock_client.get_dashboard.side_effect = ValueError("boom")
        ret = run_cli(["get", "dashboards/d1"], client=self.mock_client)
        self.assertEqual(ret, 1)

    def test_get_data(self) -> None:
        pdata = messages_pb2.ChartData.PlotData(
            plot_id="kpi",
            summary_data=messages_pb2.ChartData.SummaryBoxValue(latest_value=0.1234),
        )
        cdata = messages_pb2.ChartData(
            name="experiments/1001/charts/c1", xid=1001, wid=1, plots_data=[pdata]
        )
        self.mock_client.get_chart_data.return_value = cdata

        ret = run_cli(["get-data", "experiments/1001/charts/c1"], client=self.mock_client)
        self.assertEqual(ret, 0)
        self.mock_client.get_chart_data.assert_called_once_with("experiments/1001/charts/c1")

    def test_list(self) -> None:
        self.mock_client.list_dashboards.return_value = [
            messages_pb2.Dashboard(name="dashboards/1", title="D1")
        ]
        ret = run_cli(["list"], client=self.mock_client)
        self.assertEqual(ret, 0)
        self.mock_client.list_dashboards.assert_called_once()


class CliClientLifecycleTest(unittest.TestCase):
    """Tests that run_cli owns and releases the client it creates itself."""

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

    def test_insecure_flag_forces_plaintext_channel(self) -> None:
        with patch("xdash.cli.DashboardClient") as mock_cls:
            mock_cls.return_value.list_dashboards.return_value = []
            run_cli(["--insecure", "--endpoint", "dashboards.example.com:443", "list"])
        mock_cls.assert_called_once_with(endpoint="dashboards.example.com:443", secure=False)


if __name__ == "__main__":
    unittest.main()
