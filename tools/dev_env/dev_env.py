#!/usr/bin/env python3
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

"""XMC Unified Development Environment CLI Tool.

Consolidates environment lifecycle operations for GKE deployment and upgrade
pipelines into a unified CLI entrypoint.
"""

import argparse
import logging
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import (  # noqa: E402
    ensure_venv,
    log_error,
)
from tools.dev_env import deploy_dev_env, upgrade_dev_env  # noqa: E402


def handle_gke_deploy(args: argparse.Namespace) -> None:
    """Handler for GKE deployment workflow."""
    deploy_dev_env.run_deployment(args)


def handle_gke_upgrade(args: argparse.Namespace) -> None:
    """Handler for GKE upgrade workflow."""
    upgrade_dev_env.run_upgrade(args)


def handle_deploy(args: argparse.Namespace) -> None:
    """Handler for generic deploy command routing to deployment targets."""
    target = getattr(args, "target", "gke")
    if target != "gke":
        log_error(f"Unsupported deployment target: {target}")
        sys.exit(1)
    if not getattr(args, "dns_project", None) or not getattr(args, "dns_zone", None):
        log_error("--dns-project and --dns-zone are required when --target is gke")
        sys.exit(1)
    if not getattr(args, "config", None):
        log_error("--config is required when --target is gke")
        sys.exit(1)
    handle_gke_deploy(args)


def handle_upgrade(args: argparse.Namespace) -> None:
    """Handler for generic upgrade command routing to upgrade targets."""
    target = getattr(args, "target", "gke")
    if target != "gke":
        log_error(f"Unsupported upgrade target: {target}")
        sys.exit(1)
    handle_gke_upgrade(args)


def _add_deploy_flags(p: argparse.ArgumentParser, *, is_gke: bool) -> None:
    """Configures CLI flags for deployment commands."""
    if not is_gke:
        p.add_argument("-t", "--target", choices=["gke"], default="gke", help="Deployment target.")
    p.add_argument("-c", "--config", required=is_gke, help="Path to deployment YAML config.")
    p.add_argument("--dns-project", required=is_gke, help="GCP Project ID hosting DNS zone.")
    p.add_argument("--dns-zone", required=is_gke, help="Cloud DNS Managed Zone name.")
    group = p.add_mutually_exclusive_group()
    group.add_argument("-s", "--start-from", choices=deploy_dev_env.STEPS, help="Start from step.")
    group.add_argument("--only", choices=deploy_dev_env.STEPS, help="Execute only step.")


def _add_upgrade_flags(p: argparse.ArgumentParser, *, is_gke: bool) -> None:
    """Configures CLI flags for upgrade commands."""
    if not is_gke:
        p.add_argument("-t", "--target", choices=["gke"], default="gke", help="Upgrade target.")
    p.add_argument("-c", "--config", default=upgrade_dev_env.DEFAULT_CONFIG, help="Config path.")
    p.add_argument("--run-migrations", action="store_true", help="Run pending migrations.")
    group = p.add_mutually_exclusive_group()
    group.add_argument("-s", "--start-from", choices=upgrade_dev_env.STEPS, help="Start from step.")
    group.add_argument("--only", choices=upgrade_dev_env.STEPS, help="Execute only step.")


def create_parser() -> argparse.ArgumentParser:
    """Creates and configures top-level argument parser for dev_env CLI."""
    parser = argparse.ArgumentParser(
        prog="dev_env",
        description="XMC Development Environment Management CLI Tool",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Examples:
  python3 tools/dev_env/dev_env.py gke-deploy --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \\
      --dns-project my-dns-proj --dns-zone my-zone
  python3 tools/dev_env/dev_env.py gke-upgrade --config deploy/ctk_v2/xmanager-cloud-deployment.yaml
""",
    )
    parser.add_argument("--version", action="version", version="%(prog)s 0.1.0")
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Enable verbose debug logging.",
    )
    parser.add_argument(
        "-q",
        "--quiet",
        action="store_true",
        help="Suppress non-error output.",
    )

    subparsers = parser.add_subparsers(
        dest="command",
        title="Available Commands",
        metavar="<command>",
        required=True,
    )

    gke_deploy_p = subparsers.add_parser(
        "gke-deploy",
        help="Deploy XMC to GKE via Cluster Toolkit.",
    )
    _add_deploy_flags(gke_deploy_p, is_gke=True)
    gke_deploy_p.set_defaults(func=handle_gke_deploy)

    gke_upgrade_p = subparsers.add_parser(
        "gke-upgrade",
        help="Upgrade existing XMC on GKE.",
    )
    _add_upgrade_flags(gke_upgrade_p, is_gke=True)
    gke_upgrade_p.set_defaults(func=handle_gke_upgrade)

    deploy_p = subparsers.add_parser(
        "deploy",
        help="Deploy XMC environment (target: gke).",
    )
    _add_deploy_flags(deploy_p, is_gke=False)
    deploy_p.set_defaults(func=handle_deploy)

    upgrade_p = subparsers.add_parser(
        "upgrade",
        help="Upgrade XMC environment (target: gke).",
    )
    _add_upgrade_flags(upgrade_p, is_gke=False)
    upgrade_p.set_defaults(func=handle_upgrade)

    return parser


def main(argv: list[str] | None = None) -> None:
    """Main entry point for dev_env CLI."""
    if argv is None:
        argv = sys.argv[1:]
    ensure_venv()
    parser = create_parser()
    if not argv:
        parser.print_help()
        sys.exit(0)
    args = parser.parse_args(argv)
    if getattr(args, "verbose", False):
        logging.basicConfig(level=logging.DEBUG)
    elif getattr(args, "quiet", False):
        logging.basicConfig(level=logging.ERROR)
    else:
        logging.basicConfig(level=logging.INFO)

    if hasattr(args, "func"):
        args.func(args)
    else:
        parser.print_help()


if __name__ == "__main__":
    main(sys.argv[1:])
