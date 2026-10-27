#!/usr/bin/env python3
# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""CLI entrypoint for migrating legacy umbrella Ingestion Gateway installs to XMC Components."""

import argparse
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[3]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.migrations.components.profile_preparer import prepare_profile_values  # noqa: E402
from tools.migrations.components.resource_handoff import execute_resource_handoff  # noqa: E402


def build_parser() -> argparse.ArgumentParser:
    """Constructs the argument parser for migrate.py."""
    parser = argparse.ArgumentParser(
        description="One-time migration tool from legacy umbrella Ingestion Gateway to Components.",
    )
    sub = parser.add_subparsers(dest="subcommand", required=True)
    prep = sub.add_parser(
        "prepare", help="Generate and lint profile-values.yaml from live cluster."
    )
    prep.add_argument("--release", default="xmc", help="Umbrella Helm release name (default: xmc).")
    prep.add_argument("-n", "--namespace", default="xmc", help="Target Kubernetes namespace.")
    prep.add_argument("-o", "--output", default="profile-values.yaml", help="Output YAML path.")
    prep.add_argument(
        "--profile-dir",
        default=str(_REPO_ROOT / "deploy" / "profiles" / "stable-2026a"),
        help="Path to the stable-2026a profile chart directory.",
    )
    hoff = sub.add_parser("handoff", help="Annotate live gateway resources for Helm adoption.")
    hoff.add_argument("--release", default="xmc", help="Umbrella Helm release name (default: xmc).")
    hoff.add_argument("-n", "--namespace", default="xmc", help="Target Kubernetes namespace.")
    hoff.add_argument(
        "--undo", action="store_true", help="Revert handoff annotations before upgrade."
    )
    return parser


def dispatch(args: argparse.Namespace) -> dict[str, Any]:
    """Routes parsed CLI arguments to the requested migration subcommand."""
    if args.subcommand == "prepare":
        return prepare_profile_values(
            args.release, args.namespace, Path(args.output), Path(args.profile_dir), _REPO_ROOT
        )
    if args.subcommand == "handoff":
        return execute_resource_handoff(args.release, args.namespace, undo=args.undo)
    raise HandledError(f"Unsupported subcommand: {args.subcommand}", "Use 'prepare' or 'handoff'.")


if __name__ == "__main__":
    run_tool(build_parser(), dispatch)
