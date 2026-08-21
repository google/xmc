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
"""Command-line interface (CLI) for managing XMC Dashboards and Charts."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence

from xdash.builders import (
    ChartBuilder,
    DashboardBuilder,
    scatter_plot,
    summary_kpi_plot,
)
from xdash.client import DashboardClient, XDashError
from xdash.presets import (
    create_standard_experiment_chart,
    create_vcc_comparison_dashboard,
)


def _build_parser() -> argparse.ArgumentParser:
    """Builds the top-level argument parser and subcommands."""
    parser = argparse.ArgumentParser(
        prog="xdash",
        description="XMC Python CLI for Metrics Dashboards and Charts",
    )
    parser.add_argument(
        "--endpoint",
        default=None,
        help="Dashboard Service gRPC endpoint (default: localhost:8080 or XMC_DASHBOARD_ENDPOINT)",
    )
    subparsers = parser.add_subparsers(dest="command", help="Available subcommands")

    # create-preset subcommand
    preset_parser = subparsers.add_parser(
        "create-preset",
        help="Create standard VCC comparison dashboard using test data experiments",
    )
    preset_parser.add_argument(
        "--dashboard-id",
        default="vcc_comparison",
        help="Resource ID slug for the preset dashboard",
    )
    preset_parser.add_argument(
        "--title",
        default="VCC baseline tune comparison",
        help="Display title for the preset dashboard",
    )

    # create-dashboard subcommand
    create_dash_parser = subparsers.add_parser(
        "create-dashboard",
        help="Create a new dashboard with optional experiment charts",
    )
    create_dash_parser.add_argument("--title", required=True, help="Dashboard title")
    create_dash_parser.add_argument("--dashboard-id", default="", help="Custom dashboard ID slug")
    create_dash_parser.add_argument("--description", default="", help="Dashboard description")
    create_dash_parser.add_argument(
        "--xids",
        default="",
        help="Comma-separated experiment IDs to include standard comparison charts for",
    )

    # create-chart subcommand
    create_chart_parser = subparsers.add_parser(
        "create-chart",
        help="Create a new chart for an experiment work unit",
    )
    create_chart_parser.add_argument(
        "--parent", required=True, help="Parent dashboard resource name (e.g. dashboards/123)"
    )
    create_chart_parser.add_argument("--xid", type=int, required=True, help="Experiment ID")
    create_chart_parser.add_argument("--wid", type=int, default=1, help="Work unit ID (default: 1)")
    create_chart_parser.add_argument("--title", required=True, help="Chart title")
    create_chart_parser.add_argument(
        "--metrics",
        default="val/loss,train/accuracy",
        help="Comma-separated list of metrics to plot",
    )

    # get subcommand
    get_parser = subparsers.add_parser(
        "get",
        help="Retrieve a dashboard or chart by resource name",
    )
    get_parser.add_argument(
        "name",
        help="Resource name (e.g. dashboards/vcc_comparison or experiments/234911122/charts/baseline)",
    )

    # get-data subcommand
    get_data_parser = subparsers.add_parser(
        "get-data",
        help="Fetch evaluated metrics data for a chart",
    )
    get_data_parser.add_argument("name", help="Chart resource name")

    # list subcommand
    subparsers.add_parser("list", help="List all dashboards")

    return parser


def _handle_create_preset(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the create-preset subcommand."""
    dashboard = create_vcc_comparison_dashboard(
        dashboard_id=args.dashboard_id,
        title=args.title,
    )
    created = client.create_dashboard(dashboard, dashboard_id=args.dashboard_id)
    sys.stdout.write(
        f"Created VCC preset dashboard: {created.name} ({len(created.charts)} charts)\n"
    )
    return 0


def _handle_create_dashboard(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the create-dashboard subcommand."""
    dash_id = args.dashboard_id
    dash_name = f"dashboards/{dash_id}" if dash_id else None
    builder = DashboardBuilder(name=dash_name).set_title(args.title)
    if args.description:
        builder.set_description(args.description)

    if args.xids:
        for xid_str in args.xids.split(","):
            xid_val = int(xid_str.strip())
            if xid_val > 0:
                chart = create_standard_experiment_chart(
                    xid=xid_val, wid=1, chart_id=f"exp_{xid_val}"
                )
                builder.add_chart(chart)

    created = client.create_dashboard(builder.build(), dashboard_id=dash_id or None)
    sys.stdout.write(f"Created dashboard: {created.name} (title: {created.title})\n")
    return 0


def _handle_create_chart(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the create-chart subcommand."""
    metrics = [m.strip() for m in args.metrics.split(",") if m.strip()]
    if not metrics:
        raise ValueError("At least one metric must be provided in --metrics")

    builder = ChartBuilder().for_experiment(xid=args.xid, wid=args.wid).set_title(args.title)
    for idx, metric in enumerate(metrics):
        builder.add_plot(summary_kpi_plot(f"kpi_{idx}", f"KPI {metric}", [metric]))
        builder.add_plot(scatter_plot(f"scatter_{idx}", f"{metric} vs steps", [metric]))

    chart = builder.build()
    created = client.create_chart(parent=args.parent, chart=chart)
    sys.stdout.write(f"Created chart: {created.name} (title: {created.title})\n")
    return 0


def _handle_get(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the get subcommand."""
    name = args.name
    if name.startswith("dashboards/"):
        dash = client.get_dashboard(name)
        sys.stdout.write(
            f"Dashboard: {dash.name}\n"
            f"Title:       {dash.title}\n"
            f"Description: {dash.description}\n"
            f"Charts:      {len(dash.charts)}\n"
        )
    else:
        chart = client.get_chart(name)
        sys.stdout.write(
            f"Chart:       {chart.name}\n"
            f"Title:       {chart.title}\n"
            f"Experiment:  {chart.xid} (wid: {chart.wid})\n"
            f"Plots:       {len(chart.plots)}\n"
        )
    return 0


def _handle_get_data(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the get-data subcommand."""
    data = client.get_chart_data(args.name)
    sys.stdout.write(
        f"ChartData for {data.name} (xid={data.xid}, wid={data.wid}):\n"
        f"Total plots data returned: {len(data.plots_data)}\n"
    )
    for pdata in data.plots_data:
        sys.stdout.write(f"  - Plot [{pdata.plot_id}]: ")
        if pdata.HasField("summary_data"):
            val = pdata.summary_data.latest_value
            sys.stdout.write(f"Summary value = {val:.4f}\n")
        elif pdata.HasField("scatter_series"):
            pts = len(pdata.scatter_series.points)
            sys.stdout.write(f"Scatter series = {pts} points\n")
        elif pdata.HasField("bar_data"):
            buckets = len(pdata.bar_data.buckets)
            sys.stdout.write(f"Bar buckets = {buckets}\n")
        else:
            sys.stdout.write("Empty content\n")
    return 0


def _handle_list(client: DashboardClient) -> int:
    """Handles the list subcommand."""
    dashboards = client.list_dashboards()
    sys.stdout.write(f"Found {len(dashboards)} dashboards:\n")
    for dash in dashboards:
        sys.stdout.write(f"  - [{dash.name}] {dash.title}\n")
    return 0


def run_cli(
    argv: Sequence[str] | None = None,
    client: DashboardClient | None = None,
) -> int:
    """Executes the xdash CLI with the given arguments."""
    parser = _build_parser()
    args = parser.parse_args(argv)
    if not args.command:
        parser.print_help()
        return 1

    if client is None:
        client = DashboardClient(endpoint=args.endpoint)

    try:
        if args.command == "create-preset":
            return _handle_create_preset(client, args)
        if args.command == "create-dashboard":
            return _handle_create_dashboard(client, args)
        if args.command == "create-chart":
            return _handle_create_chart(client, args)
        if args.command == "get":
            return _handle_get(client, args)
        if args.command == "get-data":
            return _handle_get_data(client, args)
        if args.command == "list":
            return _handle_list(client)
    except (XDashError, ValueError) as err:
        sys.stderr.write(f"Error: {err}\n")
        return 1

    return 0


def main() -> None:
    """CLI entrypoint."""
    sys.exit(run_cli())
