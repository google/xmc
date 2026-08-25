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
"""Ready-to-use templates and test data presets for dashboards and charts."""

from __future__ import annotations

from collections.abc import Sequence

from xdash._protos import messages_pb2
from xdash.builders import (
    ChartBuilder,
    DashboardBuilder,
    chart_id_from_name,
    scatter_plot,
    summary_kpi_plot,
)

# Standard test experiment IDs available in UI and DataEngine test data.
VCC_BASELINE_XID = 234911122
VCC_TUNED_V1_XID = 234911123
VCC_TUNED_V2_XID = 234911124

# Experiment ID, chart ID slug, and display title for each run in the VCC preset.
VCC_COMPARISON_RUNS = (
    (VCC_BASELINE_XID, "baseline", "VCC Baseline"),
    (VCC_TUNED_V1_XID, "tuned_v1", "VCC tuned_v1"),
    (VCC_TUNED_V2_XID, "tuned_v2", "VCC tuned_v2"),
)


def create_standard_experiment_chart(
    xid: int,
    wid: int = 1,
    chart_id: str = "metrics_overview",
    title: str | None = None,
) -> messages_pb2.Chart:
    """Creates a standard 4-plot KPI & scatter chart for an experiment work unit.

    Includes validation loss and training accuracy KPIs and scatter plots matching
    XMC UI and DataEngine test data schemas.

    Args:
        xid: Experiment ID (e.g., 234911122).
        wid: Work unit ID (defaults to 1).
        chart_id: Short chart identifier slug.
        title: Optional custom chart title.

    Returns:
        A fully configured messages_pb2.Chart message.
    """
    chart_title = title or f"Experiment {xid} Overview"
    builder = (
        ChartBuilder(name=f"experiments/{xid}/workUnits/{wid}/charts/{chart_id}")
        .for_experiment(xid=xid, wid=wid)
        .set_title(chart_title)
        .set_grid_layout(columns=2)
        .add_plot(summary_kpi_plot("val_loss_kpi", "Last validation loss", ["val/loss"]))
        .add_plot(summary_kpi_plot("train_acc_kpi", "Last training accuracy", ["train/accuracy"]))
        .add_plot(scatter_plot("loss_vs_steps", "Validation loss vs steps", ["val/loss"]))
        .add_plot(scatter_plot("acc_vs_steps", "Training accuracy vs steps", ["train/accuracy"]))
    )
    return builder.build()


def create_vcc_comparison_charts() -> list[messages_pb2.Chart]:
    """Builds the three standard VCC comparison charts, one per experiment run.

    These must be created with DashboardClient.create_chart() before a dashboard can
    reference them: CreateDashboard records chart references but never creates charts.

    Returns:
        A list of messages_pb2.Chart messages in baseline, tuned_v1, tuned_v2 order.
    """
    return [
        create_standard_experiment_chart(xid=xid, wid=1, chart_id=chart_slug, title=exp_title)
        for xid, chart_slug, exp_title in VCC_COMPARISON_RUNS
    ]


def create_vcc_comparison_dashboard(
    dashboard_id: str = "vcc_comparison",
    title: str = "VCC baseline tune comparison",
    chart_ids: Sequence[str] | None = None,
) -> messages_pb2.Dashboard:
    """Creates the standard 3-experiment VCC comparison dashboard.

    Args:
        dashboard_id: Resource identifier suffix for the dashboard.
        title: Human-readable title for the dashboard.
        chart_ids: Raw IDs of already-created charts to reference. Defaults to the
            preset slugs from VCC_COMPARISON_RUNS, which match what create_chart()
            assigns for the charts from create_vcc_comparison_charts(). Pass the IDs
            returned by the server whenever it assigned them itself.

    Returns:
        A populated messages_pb2.Dashboard message referencing the comparison charts.
    """
    if chart_ids is None:
        chart_ids = [chart_id_from_name(chart.name) for chart in create_vcc_comparison_charts()]
    builder = (
        DashboardBuilder(name=f"dashboards/{dashboard_id}")
        .set_title(title)
        .set_description("Comparative evaluation of VCC baseline tuning across hyperparameter runs")
        .set_grid_layout(columns=2)
    )
    for chart_id in chart_ids:
        builder.add_chart_id(chart_id)
    return builder.build()
