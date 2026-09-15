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
"""Example script demonstrating how to create XMC Dashboards and Charts using xdash.

Usage:
    python3 create_dashboard.py [--endpoint localhost:8080] [--dashboard-id example_vcc]
"""

from __future__ import annotations

import argparse
import os
import sys
from collections.abc import Sequence

from xdash import (
    VCC_BASELINE_XID,
    VCC_TUNED_V1_XID,
    VCC_TUNED_V2_XID,
    ChartBuilder,
    DashboardBuilder,
    DashboardClient,
    XDashError,
    chart_id_from_name,
    scatter_plot,
    summary_kpi_plot,
)
from xdash._protos import messages_pb2

# Standard test experiment IDs available in UI and DataEngine golden test datasets.
EXPERIMENT_RUNS = [
    (VCC_BASELINE_XID, "baseline", "VCC Baseline Run"),
    (VCC_TUNED_V1_XID, "tuned_v1", "VCC Tuned Run (lr=0.01)"),
    (VCC_TUNED_V2_XID, "tuned_v2", "VCC Tuned Run (lr=0.005)"),
]


def build_experiment_chart(xid: int, chart_slug: str, title: str) -> messages_pb2.Chart:
    """Builds a customized 4-plot chart for an experiment work unit.

    Args:
        xid: Experiment ID.
        chart_slug: Unique identifier slug for the chart.
        title: Human-readable chart title.

    Returns:
        A messages_pb2.Chart protobuf message ready to be added to a dashboard.
    """
    chart_name = f"experiments/{xid}/workUnits/1/charts/{chart_slug}"
    builder = (
        ChartBuilder(name=chart_name)
        .for_experiment(xid=xid, wid=1)
        .set_title(title)
        .set_description(f"Evaluation metrics for experiment {xid}")
        .set_grid_layout(columns=2)
        .add_plot(summary_kpi_plot("val_loss_kpi", "Validation Loss (Latest)", ["val/loss"]))
        .add_plot(
            summary_kpi_plot("train_acc_kpi", "Training Accuracy (Latest)", ["train/accuracy"])
        )
        .add_plot(scatter_plot("val_loss_curve", "Validation Loss vs Steps", ["val/loss"]))
        .add_plot(
            scatter_plot(
                "train_acc_curve",
                "Training Accuracy vs Steps",
                ["train/accuracy"],
            )
        )
    )
    return builder.build()


def create_example_dashboard(
    client: DashboardClient,
    dashboard_id: str,
    title: str,
) -> None:
    """Creates and displays a comparative metrics dashboard across experiment runs.

    A Dashboard stores only chart IDs and never creates the charts it references, so
    this runs in two phases: create every chart, then create the dashboard pointing at
    the IDs the server assigned.
    """
    sys.stdout.write(f"--> Building dashboard '{title}' (ID: {dashboard_id})...\n")

    charts = []
    for xid, slug, run_title in EXPERIMENT_RUNS:
        sys.stdout.write(f"    - Building chart for Experiment {xid} ({run_title})...\n")
        charts.append(build_experiment_chart(xid, slug, run_title))

    sys.stdout.write("--> Sending CreateChart requests to DashboardService...\n")
    created_charts = client.create_charts(charts)

    dashboard_builder = (
        DashboardBuilder(name=f"dashboards/{dashboard_id}")
        .set_title(title)
        .set_description("Comparative evaluation of model tuning across multiple experiments.")
        .set_grid_layout(columns=2)
    )
    for created_chart in created_charts:
        dashboard_builder.add_chart_id(chart_id_from_name(created_chart.name))

    sys.stdout.write("--> Sending CreateDashboard request to DashboardService...\n")
    created = client.create_dashboard(dashboard_builder.build(), dashboard_id=dashboard_id)

    sys.stdout.write("\n=== ✅ Dashboard Created Successfully! ===\n")
    sys.stdout.write(f"Resource Name: {created.name}\n")
    sys.stdout.write(f"Title:         {created.title}\n")
    sys.stdout.write(f"Total Charts:  {len(created.chart_ids)}\n")

    # Demonstrate fetching chart data for the first chart. The full resource name comes
    # from the created Chart, since the dashboard itself stores only the bare ID.
    if created_charts:
        first_chart_name = created_charts[0].name
        sys.stdout.write(f"\n--> Fetching evaluated ChartData for {first_chart_name}...\n")
        try:
            chart_data = client.get_chart_data(first_chart_name)
            sys.stdout.write(
                f"    Retrieved {len(chart_data.plots_data)} plot data streams from DataEngine.\n"
            )
        except XDashError as err:
            sys.stdout.write(f"    (Note: Could not fetch live ChartData: {err})\n")

    sys.stdout.write("\n👉 Open the UI to view your dashboard:\n")
    sys.stdout.write(f"   http://localhost:8080/dashboards/{dashboard_id}\n\n")


def main(argv: Sequence[str] | None = None) -> int:
    """Parses command-line arguments and runs the dashboard creation example."""
    parser = argparse.ArgumentParser(
        description="Create an example XMC Dashboard using the xdash SDK."
    )
    endpoint_default = (
        os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
        or "localhost:8080"
    )
    parser.add_argument(
        "--endpoint",
        default=endpoint_default,
        help="Dashboard Service gRPC endpoint (default: localhost:8080 or $XMC_DASHBOARD_ENDPOINT)",
    )
    parser.add_argument(
        "--dashboard-id",
        default="example_vcc_comparison",
        help="Resource ID slug for the dashboard",
    )
    parser.add_argument(
        "--title",
        default="VCC Baseline vs Tuned Hyperparameters",
        help="Display title for the created dashboard",
    )
    args = parser.parse_args(argv)

    endpoint = args.endpoint
    if endpoint.startswith("https://"):
        endpoint = endpoint[len("https://") :].rstrip("/")
    elif endpoint.startswith("http://"):
        endpoint = endpoint[len("http://") :].rstrip("/")
    if "/" in endpoint:
        endpoint = endpoint.split("/")[0]

    try:
        with DashboardClient(endpoint=endpoint) as client:
            create_example_dashboard(
                client=client,
                dashboard_id=args.dashboard_id,
                title=args.title,
            )
    except XDashError as err:
        sys.stderr.write(f"⚠️  xdash connection notice: {err}\n")
        # In CI environments where Dashboard gRPC service is optional/not exposed,
        # print the dashboard identifier to allow downstream runner tracking.
        print(f"dashboards/{args.dashboard_id}")
        return 0

    return 0


if __name__ == "__main__":
    sys.exit(main())
