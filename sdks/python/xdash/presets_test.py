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
    create_vcc_comparison_dashboard,
)


class PresetsTest(unittest.TestCase):
    """Tests for preset chart and dashboard creation."""

    def test_create_standard_experiment_chart(self) -> None:
        chart = create_standard_experiment_chart(xid=VCC_BASELINE_XID, wid=1)
        self.assertEqual(chart.xid, VCC_BASELINE_XID)
        self.assertEqual(chart.wid, 1)
        self.assertEqual(len(chart.plots), 4)
        plot_ids = [p.plot_id for p in chart.plots]
        self.assertEqual(
            plot_ids,
            ["val_loss_kpi", "train_acc_kpi", "loss_vs_steps", "acc_vs_steps"],
        )

    def test_create_vcc_comparison_dashboard(self) -> None:
        dash = create_vcc_comparison_dashboard(dashboard_id="vcc_test", title="VCC Comparison Test")
        self.assertEqual(dash.name, "dashboards/vcc_test")
        self.assertEqual(dash.title, "VCC Comparison Test")
        self.assertEqual(len(dash.charts), 3)

        xids = [chart.xid for chart in dash.charts]
        self.assertEqual(xids, [VCC_BASELINE_XID, VCC_TUNED_V1_XID, VCC_TUNED_V2_XID])


if __name__ == "__main__":
    unittest.main()
