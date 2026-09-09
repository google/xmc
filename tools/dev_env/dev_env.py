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
pipelines into a unified command-line entrypoint with configurable settings.
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
from tools.dev_env import config, deploy_dev_env, upgrade_dev_env  # noqa: E402

_LOGGER = logging.getLogger("xmc.dev_env")


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


def handle_spanner(args: argparse.Namespace) -> None:
    """Handler for Cloud Spanner database operations.

    Args:
        args: Parsed command line arguments.
    """
    from tools.dev_env.spanner_manager import SpannerManager

    cfg = config.load_config(args)
    mgr = SpannerManager(cfg)
    action = getattr(args, "spanner_action", "create")
    if action in ("create", "ensure"):
        mgr.ensure_databases_exist()
    elif action == "migrate":
        mgr.apply_migrations(force=True)
    elif action == "drop":
        mgr.drop_databases()
    else:
        log_error(f"Unknown spanner action: {action}")
        sys.exit(1)


def handle_cluster(args: argparse.Namespace) -> None:
    """Handler for KinD cluster lifecycle operations.

    Args:
        args: Parsed command line arguments.
    """
    from tools.dev_env.kind_manager import KindManager

    cfg = config.load_config(args)
    mgr = KindManager(cfg)
    action = getattr(args, "cluster_action", "create")
    if action == "create":
        mgr.check_prerequisites()
        mgr.create_cluster(recreate=getattr(args, "recreate", False))
        mgr.ensure_secrets()
        mgr.apply_manifests()
    elif action == "delete":
        mgr.delete_cluster()
    else:
        log_error(f"Unknown cluster action: {action}")
        sys.exit(1)


def handle_status(args: argparse.Namespace) -> None:
    """Handler for cluster status inspection.

    Args:
        args: Parsed command line arguments.
    """
    from tools.dev_env.kind_manager import KindManager

    cfg = config.load_config(args)
    mgr = KindManager(cfg)
    mgr.get_status()


def handle_logs(args: argparse.Namespace) -> None:
    """Handler for streaming pod and service logs.

    Args:
        args: Parsed command line arguments.
    """
    from tools.dev_env.kind_manager import KindManager

    cfg = config.load_config(args)
    mgr = KindManager(cfg)
    mgr.stream_logs(
        service_name=getattr(args, "service", None),
        follow=getattr(args, "follow", False),
        lines=getattr(args, "tail", None),
        namespace=getattr(args, "namespace", "xmc"),
    )


def handle_build_images(args: argparse.Namespace) -> None:
    """Handler for compiling and loading local OCI images.

    Args:
        args: Parsed command line arguments.
    """
    from tools.dev_env.image_builder import ImageBuilder

    cfg = config.load_config(args)
    builder = ImageBuilder(cfg)
    services = getattr(args, "services", None)
    skip_build = getattr(args, "skip_build", False)
    image_tags = builder.build_images(services=services, skip_build=skip_build)
    if getattr(args, "load_kind", False):
        builder.load_images_into_kind(image_tags)


def handle_up(args: argparse.Namespace) -> None:
    """Handler for local KinD development environment bootstrap."""
    from tools.dev_env.local_runner import LocalRunner

    cfg = config.load_config(args)
    LocalRunner(cfg).up(
        recreate=getattr(args, "recreate", False),
        skip_build=getattr(args, "skip_build", False),
        skip_helm=getattr(args, "skip_helm", False),
    )


def handle_down(args: argparse.Namespace) -> None:
    """Handler for local KinD development environment teardown."""
    from tools.dev_env.local_runner import LocalRunner

    cfg = config.load_config(args)
    LocalRunner(cfg).down(drop_db=getattr(args, "drop_db", False))


def handle_reload(args: argparse.Namespace) -> None:
    """Handler for local KinD fast service reload."""
    from tools.dev_env.local_runner import LocalRunner

    cfg = config.load_config(args)
    LocalRunner(cfg).reload(
        services=getattr(args, "services", None) or None,
        watch=getattr(args, "watch", False),
        helm=getattr(args, "helm", False),
        interval=getattr(args, "interval", 1.5),
        base_ref=getattr(args, "base_ref", None),
    )


def _add_common_config_flags(p: argparse.ArgumentParser) -> None:
    """Adds common configuration override flags."""
    p.add_argument("-p", "--project-id", help="Google Cloud project ID override.")
    p.add_argument("-e", "--environment", help="Target environment override.")
    p.add_argument("--spanner-instance", help="Cloud Spanner instance override.")
    p.add_argument("--ess-db", dest="ess_database", help="ESS database override.")
    p.add_argument("--xid-db", dest="xid_database", help="XID database override.")
    p.add_argument("--cluster-name", help="Kubernetes cluster name override.")
    p.add_argument(
        "--auto-migrate",
        dest="auto_migrate",
        action="store_true",
        default=None,
        help="Enable automatic database migrations.",
    )
    p.add_argument(
        "--no-auto-migrate",
        dest="auto_migrate",
        action="store_false",
        default=None,
        help="Disable automatic database migrations.",
    )
    p.add_argument(
        "--enable-metrics",
        dest="enable_metrics",
        action="store_true",
        default=None,
        help="Enable metrics ingestion service.",
    )
    p.add_argument(
        "--no-enable-metrics",
        dest="enable_metrics",
        action="store_false",
        default=None,
        help="Disable metrics ingestion service.",
    )
    p.add_argument(
        "--enable-components",
        dest="enable_components",
        action="store_true",
        default=None,
        help="Enable components service.",
    )
    p.add_argument(
        "--no-enable-components",
        dest="enable_components",
        action="store_false",
        default=None,
        help="Disable components service.",
    )
    p.add_argument(
        "--rod-deployment-enabled",
        dest="rod_deployment_enabled",
        action="store_true",
        default=None,
        help="Enable ROD data provider service.",
    )
    p.add_argument(
        "--no-rod-deployment-enabled",
        dest="rod_deployment_enabled",
        action="store_false",
        default=None,
        help="Disable ROD data provider service.",
    )
    p.add_argument(
        "--enable-health-service",
        dest="enable_health_service",
        action="store_true",
        default=None,
        help="Enable health monitoring service.",
    )
    p.add_argument(
        "--no-enable-health-service",
        dest="enable_health_service",
        action="store_false",
        default=None,
        help="Disable health monitoring service.",
    )
    p.add_argument(
        "--enable-dashboard-service",
        dest="enable_dashboard_service",
        action="store_true",
        default=None,
        help="Enable dashboard service.",
    )
    p.add_argument(
        "--no-enable-dashboard-service",
        dest="enable_dashboard_service",
        action="store_false",
        default=None,
        help="Disable dashboard service.",
    )
    p.add_argument(
        "--enable-managed-mldiagnostics",
        dest="enable_managed_mldiagnostics",
        action="store_true",
        default=None,
        help="Enable Google Cloud Managed ML Diagnostics.",
    )
    p.add_argument(
        "--no-enable-managed-mldiagnostics",
        dest="enable_managed_mldiagnostics",
        action="store_false",
        default=None,
        help="Disable Google Cloud Managed ML Diagnostics.",
    )
    p.add_argument(
        "--managed-xprof-gcs-bucket",
        dest="managed_xprof_gcs_bucket",
        default=None,
        help="GCS bucket URL for Managed XProf profiling data (e.g. gs://my-bucket).",
    )
    p.add_argument(
        "--remote-k8s",
        "--remote-k8s-context",
        dest="remote_k8s_context",
        nargs="?",
        const="true",
        default=None,
        help="Target remote Kubernetes cluster context for hybrid workload execution.",
    )
    p.add_argument(
        "--remote-k8s-namespace",
        dest="remote_k8s_namespace",
        default=None,
        help="Namespace on remote Kubernetes cluster (default: 'xmc').",
    )
    p.add_argument(
        "--remote-k8s-service-account",
        dest="remote_k8s_service_account",
        default=None,
        help="ServiceAccount on remote cluster for token generation.",
    )
    p.add_argument(
        "--gcp-service-account",
        dest="gcp_service_account",
        default=None,
        help="GCP IAM Service Account email for workload identity.",
    )


def _add_deploy_flags(p: argparse.ArgumentParser, *, is_gke: bool) -> None:
    """Configures CLI flags for deployment commands."""
    if not is_gke:
        p.add_argument("-t", "--target", choices=["gke"], default="gke", help="Deployment target.")
    p.add_argument("-c", "--config", required=is_gke, help="Path to deployment YAML config.")
    _add_common_config_flags(p)
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
    _add_common_config_flags(p)
    p.add_argument("--run-migrations", action="store_true", help="Run pending migrations.")
    group = p.add_mutually_exclusive_group()
    group.add_argument("-s", "--start-from", choices=upgrade_dev_env.STEPS, help="Start from step.")
    group.add_argument("--only", choices=upgrade_dev_env.STEPS, help="Execute only step.")


def _add_spanner_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'spanner' subcommand parser for database management."""
    p = subparsers.add_parser(
        "spanner",
        help="Manage Cloud Spanner databases (create, migrate, drop).",
        description="Creates, migrates, or drops Cloud Spanner databases for XMC.",
    )
    p.add_argument(
        "spanner_action",
        nargs="?",
        default="create",
        choices=["create", "ensure", "migrate", "drop"],
        help="Action to perform on Cloud Spanner databases (default: create).",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_spanner)


def _add_cluster_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'cluster' subcommand parser for KinD cluster management."""
    p = subparsers.add_parser(
        "cluster",
        help="Manage local KinD Kubernetes cluster (create, delete).",
        description="Provisions or deletes local KinD cluster and configures secrets.",
    )
    p.add_argument(
        "cluster_action",
        nargs="?",
        default="create",
        choices=["create", "delete"],
        help="Action to perform on KinD cluster (default: create).",
    )
    p.add_argument(
        "--recreate",
        action="store_true",
        help="Delete existing cluster and recreate if already present.",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_cluster)


def _add_status_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'status' subcommand parser for cluster inspection."""
    p = subparsers.add_parser(
        "status",
        help="Inspect KinD cluster, pod health, and local endpoints.",
        description="Checks cluster status, namespace pods, and endpoint reachability.",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_status)


def _add_logs_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'logs' subcommand parser for pod log streaming."""
    p = subparsers.add_parser(
        "logs",
        help="Stream logs from local development services.",
        description="Streams or tails container logs for XMC services.",
    )
    p.add_argument(
        "service",
        nargs="?",
        default=None,
        help="Service name to stream logs for (e.g., actuator, reconciler, ess, xid).",
    )
    p.add_argument("-f", "--follow", action="store_true", help="Stream logs continuously.")
    p.add_argument(
        "--tail",
        type=int,
        default=None,
        help="Lines of recent log history to show (default: 100 when not following).",
    )
    p.add_argument("-n", "--namespace", default="xmc", help="Target namespace (default: xmc).")
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_logs)


def _add_build_images_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'build-images' subcommand parser for local image compilation."""
    p = subparsers.add_parser(
        "build-images",
        help="Build Bazel OCI container images and load into Docker / KinD.",
        description="Compiles OCI container images in parallel for enabled microservices.",
    )
    p.add_argument(
        "services",
        nargs="*",
        default=None,
        help="Specific microservices to compile (e.g. reconciler ui). Default is all enabled.",
    )
    p.add_argument(
        "--load-kind",
        action="store_true",
        help="Also load compiled container images into the local KinD cluster.",
    )
    p.add_argument(
        "--skip-build",
        action="store_true",
        help="Skip compilation step and only load existing local Docker images.",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_build_images)


def _add_up_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'up' subcommand parser for full local environment provisioning."""
    p = subparsers.add_parser("up", help="Bootstrap local KinD environment.")
    p.add_argument(
        "--recreate-cluster",
        "--recreate",
        dest="recreate",
        action="store_true",
        help="Recreate cluster if present.",
    )
    p.add_argument(
        "--no-build-images",
        "--skip-build",
        dest="skip_build",
        action="store_true",
        help="Skip Bazel image compilation.",
    )
    p.add_argument(
        "--no-deploy-helm",
        "--skip-helm",
        dest="skip_helm",
        action="store_true",
        help="Skip Helm chart deployment.",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_up)


def _add_down_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'down' subcommand parser for local environment teardown."""
    p = subparsers.add_parser("down", help="Tear down local KinD environment.")
    p.add_argument("--drop-db", action="store_true", help="Drop Spanner dev databases.")
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_down)


def _add_reload_parser(subparsers: argparse._SubParsersAction) -> None:
    """Adds the 'reload' subcommand parser for fast microservice reload."""
    p = subparsers.add_parser(
        "reload",
        help="Recompile and hot-reload modified services into KinD cluster.",
        description="Fast incremental reload of modified microservices into local KinD.",
    )
    p.add_argument(
        "services",
        nargs="*",
        default=None,
        help="Services to reload (e.g. reconciler, ui). Defaults to auto-detecting changes from git.",
    )
    p.add_argument(
        "-w",
        "--watch",
        action="store_true",
        help="Continuously watch filesystem and reload on changes.",
    )
    p.add_argument(
        "--helm",
        action="store_true",
        help="Upgrade Helm release on cluster.",
    )
    p.add_argument(
        "--interval",
        type=float,
        default=1.5,
        help="Watch mode polling interval in seconds (default: 1.5).",
    )
    p.add_argument(
        "--base-ref",
        default=None,
        help="Git reference or commit to compare against for change detection.",
    )
    _add_common_config_flags(p)
    p.add_argument("-c", "--config", help="Path to deployment YAML config.")
    p.set_defaults(func=handle_reload)


def create_parser() -> argparse.ArgumentParser:
    """Creates and configures top-level argument parser for dev_env CLI."""
    parser = argparse.ArgumentParser(
        prog="dev_env",
        description="XMC Development Environment Management CLI Tool",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""Examples:
  python3 tools/dev_env/dev_env.py up --recreate
  python3 tools/dev_env/dev_env.py down --drop-db
  python3 tools/dev_env/dev_env.py reload
  python3 tools/dev_env/dev_env.py reload reconciler ui
  python3 tools/dev_env/dev_env.py reload -w
  python3 tools/dev_env/dev_env.py reload --helm
  python3 tools/dev_env/dev_env.py gke-deploy --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \\
      --dns-project my-dns-proj --dns-zone my-zone
  python3 tools/dev_env/dev_env.py gke-upgrade --config deploy/ctk_v2/xmanager-cloud-deployment.yaml
  python3 tools/dev_env/dev_env.py cluster create --cluster-name my-kind
  python3 tools/dev_env/dev_env.py cluster delete --cluster-name my-kind
  python3 tools/dev_env/dev_env.py status --cluster-name my-kind
  python3 tools/dev_env/dev_env.py logs reconciler --follow
  python3 tools/dev_env/dev_env.py build-images reconciler ui --load-kind
  python3 tools/dev_env/dev_env.py spanner create --project-id my-proj
  python3 tools/dev_env/dev_env.py spanner migrate --project-id my-proj
  python3 tools/dev_env/dev_env.py spanner drop --project-id my-proj
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

    _add_up_parser(subparsers)
    _add_down_parser(subparsers)
    _add_reload_parser(subparsers)

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

    _add_cluster_parser(subparsers)
    _add_status_parser(subparsers)
    _add_logs_parser(subparsers)
    _add_build_images_parser(subparsers)
    _add_spanner_parser(subparsers)

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
        logging.basicConfig(
            level=logging.DEBUG,
            format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        )
    elif getattr(args, "quiet", False):
        logging.basicConfig(
            level=logging.ERROR,
            format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        )
    else:
        logging.basicConfig(
            level=logging.INFO,
            format="%(message)s",
        )

    _LOGGER.debug("Executing dev_env subcommand: %s", getattr(args, "command", None))

    try:
        if hasattr(args, "func"):
            args.func(args)
        else:
            parser.print_help()
    except (ValueError, FileNotFoundError, RuntimeError) as e:
        log_error(str(e))
        sys.exit(1)


if __name__ == "__main__":
    main(sys.argv[1:])
