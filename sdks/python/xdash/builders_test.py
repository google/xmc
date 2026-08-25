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
    chart_id_from_name,
    scatter_plot,
    summary_kpi_plot,
    validate_chart_id,
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

    def test_chart_builder_requires_an_experiment(self) -> None:
        with self.assertRaises(ValueError) as cm:
            ChartBuilder().set_title("No XID Chart").add_plot(
                summary_kpi_plot("p1", "KPI", ["val/loss"])
            ).build()
        self.assertIn("xid", str(cm.exception))

    def test_chart_builder_requires_a_title(self) -> None:
        with self.assertRaises(ValueError) as cm:
            ChartBuilder().for_experiment(xid=1001).add_plot(
                summary_kpi_plot("p1", "KPI", ["val/loss"])
            ).build()
        self.assertIn("title", str(cm.exception))

    def test_chart_builder_requires_at_least_one_plot(self) -> None:
        # xid and title are both set, so the empty-plots branch is the one that fires;
        # leaving either out would have this pass on the earlier check instead.
        with self.assertRaises(ValueError) as cm:
            ChartBuilder().for_experiment(xid=1001).set_title("Has Title").build()
        self.assertIn("PlotSpec", str(cm.exception))

    def test_builder_boundary_values(self) -> None:
        with self.assertRaises(ValueError):
            ChartBuilder().for_experiment(xid=-1)
        with self.assertRaises(ValueError):
            ChartBuilder().for_experiment(xid=0)
        for bad_wid in (-1, 0):
            # workUnits/0 is rejected by CreateChart's parent validation, so the
            # builder will not produce a chart the server is certain to refuse.
            with self.assertRaises(ValueError):
                ChartBuilder().for_experiment(xid=1, wid=bad_wid)
        with self.assertRaises(ValueError):
            ChartBuilder().set_grid_layout(columns=0)
        with self.assertRaises(ValueError):
            DashboardBuilder().set_grid_layout(columns=0)

    def test_chart_builder_rejects_a_missing_work_unit_at_build(self) -> None:
        # for_experiment() is the only way to set wid, but a builder that never called
        # it must not silently emit wid=0.
        builder = ChartBuilder().set_title("C").add_plot(summary_kpi_plot("p1", "K", ["m"]))
        builder._xid = 1001
        with self.assertRaises(ValueError) as cm:
            builder.build()
        self.assertIn("wid", str(cm.exception))

    def test_none_children_are_rejected(self) -> None:
        with self.assertRaises(ValueError):
            ChartBuilder().add_plot(None)  # type: ignore[arg-type]
        with self.assertRaises(ValueError):
            DashboardBuilder().add_chart(None)  # type: ignore[arg-type]

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
        # A dashboard stores the bare chart ID, not the embedded Chart message.
        self.assertEqual(list(dash.chart_ids), ["c1"])
        self.assertEqual(dash.chart_layout.columns, 2)

    def test_add_chart_derives_id_from_resource_name(self) -> None:
        dash = (
            DashboardBuilder(name="dashboards/d1")
            .set_title("D1")
            .add_chart(
                messages_pb2.Chart(
                    name="experiments/1001/workUnits/1/charts/f47ac10b-58cc-4372-a567-0e02b2c3d479"
                )
            )
            .build()
        )
        self.assertEqual(list(dash.chart_ids), ["f47ac10b-58cc-4372-a567-0e02b2c3d479"])

    def test_add_chart_requires_a_name_to_reference(self) -> None:
        with self.assertRaises(ValueError) as cm:
            DashboardBuilder().set_title("D1").add_chart(messages_pb2.Chart(title="No name"))
        self.assertIn("chart.name", str(cm.exception))

    def test_add_chart_id_rejects_resource_names_and_blanks(self) -> None:
        builder = DashboardBuilder().set_title("D1")
        with self.assertRaises(ValueError) as cm:
            builder.add_chart_id("experiments/1001/workUnits/1/charts/c1")
        self.assertIn("cannot contain slashes", str(cm.exception))
        for blank in ("", "   ", "\t\n"):
            with self.assertRaises(ValueError):
                builder.add_chart_id(blank)

    def test_add_chart_id_rejects_duplicates(self) -> None:
        builder = DashboardBuilder().set_title("D1").add_chart_id("c1")
        with self.assertRaises(ValueError) as cm:
            builder.add_chart_id("c1")
        self.assertIn("already referenced", str(cm.exception))

    def test_chart_id_from_name_handles_trailing_slash(self) -> None:
        self.assertEqual(chart_id_from_name("dashboards/d1/charts/c1/"), "c1")
        for blank in ("", "   ", "/"):
            with self.assertRaises(ValueError):
                chart_id_from_name(blank)

    def test_chart_id_from_name_accepts_every_chart_pattern(self) -> None:
        # All three patterns the Chart resource declares, including the top-level one
        # that has no parent collection before "charts".
        for name in (
            "experiments/1001/workUnits/1/charts/c1",
            "charts/c1",
            "dashboards/d1/charts/c1",
        ):
            self.assertEqual(chart_id_from_name(name), "c1")

    def test_chart_id_from_name_rejects_names_that_are_not_charts(self) -> None:
        for name in (
            # Would silently yield "1" if only the trailing segment were taken.
            "experiments/1001/workUnits/1",
            # Would silently yield "charts", the collection name, not a chart ID.
            "experiments/1001/workUnits/1/charts",
            "experiments/1001/workUnits/1/charts/",
            "charts",
            "c1",
        ):
            with self.assertRaises(ValueError, msg=name) as cm:
                chart_id_from_name(name)
            self.assertIn("not a chart resource name", str(cm.exception))

    def test_validate_chart_id_strips_surrounding_whitespace(self) -> None:
        self.assertEqual(validate_chart_id("  c1  "), "c1")

    def test_dashboard_builder_requires_title(self) -> None:
        builder = DashboardBuilder(name="dashboards/empty")
        with self.assertRaises(ValueError):
            builder.build()


if __name__ == "__main__":
    unittest.main()
