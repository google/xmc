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
import re
import sys
from collections.abc import Sequence

from xdash.builders import (
    ChartBuilder,
    DashboardBuilder,
    chart_id_from_name,
    scatter_plot,
    summary_kpi_plot,
)
from xdash.client import DashboardClient, XDashError
from xdash.presets import (
    create_standard_experiment_chart,
    create_vcc_comparison_charts,
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
    parser.add_argument(
        "--insecure",
        action="store_true",
        help="Force a plaintext channel. TLS is used by default for non-local endpoints.",
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
        "--parent",
        required=True,
        help="Parent work unit resource name (e.g. experiments/123/workUnits/1)",
    )
    create_chart_parser.add_argument(
        "--xid", type=int, default=None, help="Experiment ID (default: taken from --parent)"
    )
    create_chart_parser.add_argument(
        "--wid", type=int, default=None, help="Work unit ID (default: taken from --parent)"
    )
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
    """Handles the create-preset subcommand.

    Charts are created first and the dashboard then references the IDs the server
    assigned, because a Dashboard only stores chart IDs and never creates the charts.
    """
    created_charts = client.create_charts(create_vcc_comparison_charts())
    chart_ids = [chart_id_from_name(chart.name) for chart in created_charts]
    dashboard = create_vcc_comparison_dashboard(
        dashboard_id=args.dashboard_id,
        title=args.title,
        chart_ids=chart_ids,
    )
    created = client.create_dashboard(dashboard, dashboard_id=args.dashboard_id)
    sys.stdout.write(
        f"Created VCC preset dashboard: {created.name} ({len(created.chart_ids)} charts)\n"
    )
    return 0


def _handle_create_dashboard(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the create-dashboard subcommand."""
    dash_id = args.dashboard_id
    dash_name = f"dashboards/{dash_id}" if dash_id else None
    builder = DashboardBuilder(name=dash_name).set_title(args.title)
    if args.description:
        builder.set_description(args.description)

    charts = []
    if args.xids:
        for xid_str in args.xids.split(","):
            xid_str = xid_str.strip()
            if not xid_str:
                continue
            try:
                xid_val = int(xid_str)
            except ValueError:
                raise ValueError(f"--xids must be a comma-separated list of integers: {xid_str}")
            # No guard on xid_val: ChartBuilder.for_experiment rejects a non-positive ID
            # with a clear message, rather than the dashboard silently losing a chart.
            charts.append(
                create_standard_experiment_chart(xid=xid_val, wid=1, chart_id=f"exp_{xid_val}")
            )

    # Charts must exist before the dashboard can reference them by ID.
    for created_chart in client.create_charts(charts):
        builder.add_chart_id(chart_id_from_name(created_chart.name))

    created = client.create_dashboard(builder.build(), dashboard_id=dash_id or None)
    sys.stdout.write(f"Created dashboard: {created.name} (title: {created.title})\n")
    return 0


_WORK_UNIT_PARENT = re.compile(r"^experiments/(\d+)/workUnits/(\d+)$")


def _resolve_work_unit(parent: str, xid: int | None, wid: int | None) -> tuple[int, int]:
    """Derives the experiment and work unit IDs for create-chart from --parent.

    Args:
        parent: Work unit resource name, e.g. 'experiments/123/workUnits/1'.
        xid: Experiment ID given on the command line, or None to take it from parent.
        wid: Work unit ID given on the command line, or None to take it from parent.

    Returns:
        The (xid, wid) pair to stamp on the chart.

    Raises:
        ValueError: If parent is not a work unit resource name, or an explicit
            --xid/--wid disagrees with the IDs embedded in it.
    """
    match = _WORK_UNIT_PARENT.match(parent)
    if not match:
        raise ValueError(
            "--parent must be a work unit resource name "
            f"('experiments/{{xid}}/workUnits/{{wid}}'), got: {parent}"
        )
    parent_xid, parent_wid = int(match.group(1)), int(match.group(2))
    if xid is not None and xid != parent_xid:
        raise ValueError(f"--xid ({xid}) does not match the experiment in --parent ({parent_xid})")
    if wid is not None and wid != parent_wid:
        raise ValueError(f"--wid ({wid}) does not match the work unit in --parent ({parent_wid})")
    return parent_xid, parent_wid


def _handle_create_chart(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the create-chart subcommand."""
    xid, wid = _resolve_work_unit(args.parent, args.xid, args.wid)
    metrics = [m.strip() for m in args.metrics.split(",") if m.strip()]
    if not metrics:
        raise ValueError("At least one metric must be provided in --metrics")

    builder = ChartBuilder().for_experiment(xid=xid, wid=wid).set_title(args.title)
    for idx, metric in enumerate(metrics):
        builder.add_plot(summary_kpi_plot(f"kpi_{idx}", f"KPI {metric}", [metric]))
        builder.add_plot(scatter_plot(f"scatter_{idx}", f"{metric} vs steps", [metric]))

    chart = builder.build()
    created = client.create_chart(parent=args.parent, chart=chart)
    sys.stdout.write(f"Created chart: {created.name} (title: {created.title})\n")
    return 0


def _handle_get(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the get subcommand.

    Raises:
        ValueError: If the resource name is blank or is not a dashboard or chart name.
    """
    name = args.name.strip()
    if not name:
        raise ValueError("Resource name cannot be blank")
    if not name.startswith(("dashboards/", "experiments/")):
        raise ValueError(
            f"Unrecognised resource name '{name}'. Expected it to start with "
            "'dashboards/' or 'experiments/'."
        )

    if name.startswith("dashboards/") and "/charts/" not in name:
        dash = client.get_dashboard(name)
        sys.stdout.write(
            f"Dashboard: {dash.name}\n"
            f"Title:       {dash.title}\n"
            f"Description: {dash.description}\n"
            f"Charts:      {len(dash.chart_ids)}\n"
        )
        return 0

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
        # One WhichOneof over the 'content' oneof, rather than a HasField per member:
        # a new content type then falls through to "Empty content" instead of being
        # silently skipped by a chain nobody remembered to extend.
        content = pdata.WhichOneof("content")
        if content == "summary_data":
            sys.stdout.write(f"Summary value = {pdata.summary_data.latest_value:.4f}\n")
        elif content == "scatter_series":
            sys.stdout.write(f"Scatter series = {len(pdata.scatter_series.points)} points\n")
        elif content == "bar_data":
            sys.stdout.write(f"Bar buckets = {len(pdata.bar_data.buckets)}\n")
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

    owned_client = None
    try:
        # Inside the try: opening the channel can raise XDashConnectionError, and the
        # user deserves the same "Error: ..." line for that as for a failed RPC.
        if client is None:
            owned_client = DashboardClient(
                endpoint=args.endpoint, secure=False if args.insecure else None
            )
            client = owned_client

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
    finally:
        if owned_client is not None:
            owned_client.close()

    return 0


def main() -> None:
    """CLI entrypoint."""
    sys.exit(run_cli())
