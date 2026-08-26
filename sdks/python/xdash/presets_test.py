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
"""Unit tests for xdash preset templates and test data models."""

import unittest

from xdash.presets import (
    VCC_BASELINE_XID,
    VCC_TUNED_V1_XID,
    VCC_TUNED_V2_XID,
    create_standard_experiment_chart,
    create_vcc_comparison_charts,
    create_vcc_comparison_dashboard,
)


class PresetsTest(unittest.TestCase):
    """Tests for preset chart and dashboard creation."""

    def test_create_standard_experiment_chart_defaults(self) -> None:
        chart = create_standard_experiment_chart(xid=VCC_BASELINE_XID, wid=1)
        self.assertEqual(chart.xid, VCC_BASELINE_XID)
        self.assertEqual(chart.wid, 1)
        self.assertEqual(chart.title, f"Experiment {VCC_BASELINE_XID} Overview")
        self.assertEqual(
            chart.name, f"experiments/{VCC_BASELINE_XID}/workUnits/1/charts/metrics_overview"
        )
        self.assertEqual(len(chart.plots), 4)
        plot_ids = [p.plot_id for p in chart.plots]
        self.assertEqual(
            plot_ids,
            ["val_loss_kpi", "train_acc_kpi", "loss_vs_steps", "acc_vs_steps"],
        )
        self.assertEqual(
            [list(p.data.metrics) for p in chart.plots],
            [["val/loss"], ["train/accuracy"], ["val/loss"], ["train/accuracy"]],
        )

    def test_create_standard_experiment_chart_honours_overrides(self) -> None:
        chart = create_standard_experiment_chart(
            xid=VCC_TUNED_V1_XID, wid=7, chart_id="custom_slug", title="Custom Title"
        )
        self.assertEqual(chart.title, "Custom Title")
        self.assertEqual(
            chart.name, f"experiments/{VCC_TUNED_V1_XID}/workUnits/7/charts/custom_slug"
        )
        self.assertEqual((chart.xid, chart.wid), (VCC_TUNED_V1_XID, 7))
        # The overrides change identity and title only; the plot set is the preset's.
        self.assertEqual(
            [p.plot_id for p in chart.plots],
            ["val_loss_kpi", "train_acc_kpi", "loss_vs_steps", "acc_vs_steps"],
        )

    def test_create_standard_experiment_chart_rejects_a_bad_experiment(self) -> None:
        for bad_xid in (0, -1):
            with self.assertRaises(ValueError):
                create_standard_experiment_chart(xid=bad_xid)
        with self.assertRaises(ValueError):
            create_standard_experiment_chart(xid=VCC_BASELINE_XID, wid=0)

    def test_create_vcc_comparison_charts(self) -> None:
        charts = create_vcc_comparison_charts()
        self.assertEqual(
            [chart.xid for chart in charts],
            [VCC_BASELINE_XID, VCC_TUNED_V1_XID, VCC_TUNED_V2_XID],
        )
        self.assertEqual(
            [chart.title for chart in charts],
            ["VCC Baseline", "VCC tuned_v1", "VCC tuned_v2"],
        )
        self.assertEqual(
            [chart.name.rsplit("/", 1)[-1] for chart in charts],
            ["baseline", "tuned_v1", "tuned_v2"],
        )

    def test_create_vcc_comparison_dashboard(self) -> None:
        dash = create_vcc_comparison_dashboard(dashboard_id="vcc_test", title="VCC Comparison Test")
        self.assertEqual(dash.name, "dashboards/vcc_test")
        self.assertEqual(dash.title, "VCC Comparison Test")
        # The dashboard references charts by bare ID; the preset slugs match what
        # create_chart() assigns for the charts from create_vcc_comparison_charts().
        self.assertEqual(list(dash.chart_ids), ["baseline", "tuned_v1", "tuned_v2"])

    def test_create_vcc_comparison_dashboard_accepts_server_assigned_ids(self) -> None:
        server_ids = [
            "f47ac10b-58cc-4372-a567-0e02b2c3d479",
            "9f8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
        ]
        dash = create_vcc_comparison_dashboard(chart_ids=server_ids)
        self.assertEqual(list(dash.chart_ids), server_ids)

    def test_create_vcc_comparison_dashboard_defaults(self) -> None:
        dash = create_vcc_comparison_dashboard()
        self.assertEqual(dash.name, "dashboards/vcc_comparison")
        self.assertEqual(dash.title, "VCC baseline tune comparison")
        self.assertEqual(len(dash.chart_ids), 3)


if __name__ == "__main__":
    unittest.main()
