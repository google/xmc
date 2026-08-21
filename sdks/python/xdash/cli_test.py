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
from unittest.mock import MagicMock

from xdash._protos import messages_pb2
from xdash.cli import run_cli
from xdash.presets import create_vcc_comparison_dashboard


class CliTest(unittest.TestCase):
    """Tests for xdash CLI command parsing and execution."""

    def setUp(self) -> None:
        self.mock_client = MagicMock()

    def test_no_command_returns_help_error(self) -> None:
        ret = run_cli([], client=self.mock_client)
        self.assertEqual(ret, 1)

    def test_create_preset(self) -> None:
        dash = create_vcc_comparison_dashboard()
        self.mock_client.create_dashboard.return_value = dash

        ret = run_cli(
            ["create-preset", "--dashboard-id", "test_dash", "--title", "Test Title"],
            client=self.mock_client,
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()

    def test_create_dashboard_with_xids(self) -> None:
        dash = messages_pb2.Dashboard(name="dashboards/d1", title="Dash 1")
        self.mock_client.create_dashboard.return_value = dash

        ret = run_cli(
            [
                "create-dashboard",
                "--title",
                "Dash 1",
                "--dashboard-id",
                "d1",
                "--xids",
                "234911122, 234911123, ",
            ],
            client=self.mock_client,
        )
        self.assertEqual(ret, 0)
        self.mock_client.create_dashboard.assert_called_once()

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


if __name__ == "__main__":
    unittest.main()
