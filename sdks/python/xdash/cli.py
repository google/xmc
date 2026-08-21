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

from xdash.client import DashboardClient, XDashError


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

    # get subcommand
    get_parser = subparsers.add_parser(
        "get",
        help="Retrieve a dashboard or chart by resource name",
    )
    get_parser.add_argument(
        "name",
        help="Resource name (e.g. dashboards/vcc_comparison or experiments/234911122/charts/baseline)",
    )

    # list subcommand
    subparsers.add_parser("list", help="List all dashboards")

    return parser


def _handle_get(client: DashboardClient, args: argparse.Namespace) -> int:
    """Handles the get subcommand."""
    name = args.name
    if name.startswith("dashboards/") and "/charts/" not in name:
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
    if client is None:
        owned_client = DashboardClient(
            endpoint=args.endpoint, secure=False if args.insecure else None
        )
        client = owned_client

    try:
        if args.command == "get":
            return _handle_get(client, args)
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
