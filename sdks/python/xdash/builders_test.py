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
"""Unit tests for xdash builder classes and plot helpers."""

import unittest

from xdash._protos import messages_pb2
from xdash.builders import (
    ChartBuilder,
    DashboardBuilder,
    bar_plot,
    scatter_plot,
    summary_kpi_plot,
)


class BuildersTest(unittest.TestCase):
    """Tests for ChartBuilder, DashboardBuilder, and plot creation functions."""

    def test_summary_kpi_plot(self) -> None:
        plot = summary_kpi_plot("val_kpi", "Validation KPI", ["val/loss"])
        self.assertEqual(plot.plot_id, "val_kpi")
        self.assertEqual(plot.title, "Validation KPI")
        self.assertEqual(plot.type, messages_pb2.SUMMARY_BOX)
        self.assertEqual(list(plot.data.metrics), ["val/loss"])

    def test_scatter_plot_with_custom_mapping(self) -> None:
        plot = scatter_plot(
            "loss_line", "Validation Curve", ["val/loss"], x_metric="step", y_metric="val/loss"
        )
        self.assertEqual(plot.plot_id, "loss_line")
        self.assertEqual(plot.type, messages_pb2.SCATTER)
        self.assertEqual(plot.mapping.x, "step")
        self.assertEqual(plot.mapping.y, "val/loss")

    def test_bar_plot(self) -> None:
        plot = bar_plot("bar_card", "Metric Bars", ["throughput"])
        self.assertEqual(plot.plot_id, "bar_card")
        self.assertEqual(plot.type, messages_pb2.BAR)
        self.assertEqual(list(plot.data.metrics), ["throughput"])

    def test_plot_helper_validation(self) -> None:
        with self.assertRaises(ValueError):
            summary_kpi_plot("", "Title", ["metric"])
        with self.assertRaises(ValueError):
            scatter_plot("id", "Title", [])

    def test_chart_builder_success(self) -> None:
        plot = summary_kpi_plot("kpi_1", "Loss KPI", ["val/loss"])
        chart = (
            ChartBuilder()
            .for_experiment(xid=1001, wid=1)
            .set_title("Test Chart")
            .set_description("Description text")
            .set_grid_layout(columns=3)
            .add_plot(plot)
            .build()
        )
        self.assertEqual(chart.xid, 1001)
        self.assertEqual(chart.wid, 1)
        self.assertEqual(chart.title, "Test Chart")
        self.assertEqual(chart.description, "Description text")
        self.assertEqual(chart.plot_layout.columns, 3)
        self.assertEqual(len(chart.plots), 1)
        self.assertTrue(chart.name.startswith("experiments/1001/workUnits/1/charts/"))

    def test_chart_builder_validation(self) -> None:
        builder = ChartBuilder().set_title("No XID Chart")
        with self.assertRaises(ValueError):
            builder.build()

        builder2 = ChartBuilder().for_experiment(xid=1001)
        with self.assertRaises(ValueError):
            builder2.build()  # missing title and plots

    def test_builder_boundary_values(self) -> None:
        with self.assertRaises(ValueError):
            ChartBuilder().for_experiment(xid=-1)
        with self.assertRaises(ValueError):
            ChartBuilder().for_experiment(xid=1, wid=-1)
        with self.assertRaises(ValueError):
            ChartBuilder().set_grid_layout(columns=0)
        with self.assertRaises(ValueError):
            DashboardBuilder().set_grid_layout(columns=0)

    def test_blank_titles_are_rejected(self) -> None:
        plot = summary_kpi_plot("p1", "KPI", ["val/loss"])
        for blank in ("", "   ", "\t\n"):
            with self.assertRaises(ValueError):
                ChartBuilder().for_experiment(xid=1001).set_title(blank).add_plot(plot).build()
            with self.assertRaises(ValueError):
                DashboardBuilder().set_title(blank).build()

    def test_blank_plot_arguments_are_rejected(self) -> None:
        for helper in (summary_kpi_plot, scatter_plot, bar_plot):
            with self.assertRaises(ValueError):
                helper("   ", "Title", ["val/loss"])
            with self.assertRaises(ValueError):
                helper("p1", "Title", [])
            with self.assertRaises(ValueError):
                helper("p1", "Title", ["val/loss", "  "])

    def test_dashboard_builder_success(self) -> None:
        plot = summary_kpi_plot("p1", "KPI", ["val/loss"])
        chart = (
            ChartBuilder().for_experiment(xid=1001, wid=1).set_title("C1").add_plot(plot).build()
        )
        dash = (
            DashboardBuilder(name="dashboards/my_dash")
            .set_title("My Dashboard")
            .set_description("Dashboard desc")
            .set_grid_layout(columns=2)
            .add_chart(chart)
            .build()
        )
        self.assertEqual(dash.name, "dashboards/my_dash")
        self.assertEqual(dash.title, "My Dashboard")
        self.assertEqual(dash.description, "Dashboard desc")
        self.assertEqual(len(dash.charts), 1)
        self.assertEqual(dash.chart_layout.columns, 2)

    def test_dashboard_builder_requires_title(self) -> None:
        builder = DashboardBuilder(name="dashboards/empty")
        with self.assertRaises(ValueError):
            builder.build()


if __name__ == "__main__":
    unittest.main()
