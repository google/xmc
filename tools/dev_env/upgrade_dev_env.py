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

"""XMC Environment Upgrade Tool (upgrade_dev_env.py).

Upgrades an existing deployed XManager on Cloud (XMC) GKE environment to match
the latest code in the current repository branch.
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
from dataclasses import asdict, dataclass, replace
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import (  # noqa: E402
    NC,
    RED,
    DeploymentConfig,
    build_and_push_images,
    configure_docker_auth,
    deploy_component_profile,
    ensure_venv,
    load_and_validate_config as tool_load_config,
    log_error,
    log_info,
    log_success,
    run_command,
    verify_helm_min_version,
)
from tools.dev_env.blueprint_compiler import compile_blueprint  # noqa: E402

STEPS = ["check", "migrate", "images", "app", "restart", "verify"]
DEFAULT_CONFIG = "deploy/ctk_v2/xmanager-cloud-deployment.yaml"


@dataclass(kw_only=True)
class UpgradeConfig(DeploymentConfig):
    """Holds deployment configuration values for XMC upgrade."""

    config_path: str
    run_migrations: bool = False


def error_exit(msg: str, exit_code: int = 1) -> None:
    """Prints an error message to stderr and exits."""
    log_error(msg)
    sys.exit(exit_code)


def parse_args(args: list[str] | None = None) -> argparse.Namespace:
    """Parses command-line arguments and validates mutual exclusion rules.

    Args:
      args: Command-line arguments to parse, or None for sys.argv[1:].

    Returns:
      Parsed argparse.Namespace instance.
    """
    parser = argparse.ArgumentParser(
        description=(
            "Upgrades an existing deployed XMC GKE environment to match current repository code."
        ),
        add_help=True,
    )
    parser.add_argument(
        "--config",
        "-c",
        default=DEFAULT_CONFIG,
        help="Path to deployment config YAML file.",
    )
    parser.add_argument(
        "--start-from",
        "-s",
        choices=STEPS,
        help="Start/resume upgrade from this step onwards.",
    )
    parser.add_argument(
        "--only",
        choices=STEPS,
        help="Execute ONLY this specific step and exit.",
    )
    parser.add_argument(
        "--run-migrations",
        action="store_true",
        help="Automatically run pending migrations if detected.",
    )
    parser.add_argument("--enable-components", action=argparse.BooleanOptionalAction, default=None)
    parser.add_argument(
        "--profile-values",
        default=None,
        help="Optional values YAML file passed to helm -f when deploying xmc-profile.",
    )

    parsed = parser.parse_args(args)

    if parsed.start_from and parsed.only:
        error_exit(
            "Arguments --start-from and --only are mutually exclusive.",
            exit_code=1,
        )

    return parsed


def load_config(config_file: str, run_migrations: bool = False) -> UpgradeConfig:
    """Loads YAML config file and merges with environment variable overrides.

    Args:
      config_file: Target YAML configuration file path string.
      run_migrations: Boolean flag to enable running migrations.

    Returns:
      Populated UpgradeConfig instance.
    """
    log_info(f"Loading configuration from '{config_file}'...")

    try:
        xmc_config = tool_load_config(config_file)
    except (FileNotFoundError, ValueError) as e:
        error_exit(str(e))

    return UpgradeConfig(
        config_path=config_file,
        run_migrations=run_migrations,
        **asdict(xmc_config),
    )


def step_check(config: UpgradeConfig) -> None:
    """Performs pre-flight CLI dependency checks and authenticates to GCP/GKE."""
    log_info("Running pre-flight dependency and authentication checks...")

    for binary in ["gcloud", "kubectl", "helm", "bazel"]:
        if not shutil.which(binary):
            error_exit(f"Required CLI tool '{binary}' is not installed or not in PATH.")
    if config.enable_components:
        try:
            verify_helm_min_version()
        except (subprocess.CalledProcessError, RuntimeError, ValueError) as e:
            error_exit(f"Failed Helm version check: {e}")

    gcluster_path = Path("./cluster-toolkit/gcluster")
    if not gcluster_path.is_file() or not os.access(gcluster_path, os.X_OK):
        error_exit(
            "Local cluster-toolkit binary './cluster-toolkit/gcluster' not found"
            " or is not executable."
        )

    log_info(f"Verifying gcloud target project ({config.project_id})...")
    run_command(["gcloud", "config", "set", "project", config.project_id, "--quiet"])

    configure_docker_auth(config)

    log_info(
        f"Fetching kubectl credentials for GKE cluster '{config.gke_cluster}' in"
        f" region '{config.region}'..."
    )
    run_command(
        [
            "gcloud",
            "container",
            "clusters",
            "get-credentials",
            config.gke_cluster,
            f"--region={config.region}",
            f"--project={config.project_id}",
        ]
    )

    res = run_command(
        ["kubectl", "get", "namespace", config.namespace],
        check=False,
        capture_output=True,
    )
    if res.returncode != 0:
        error_exit(
            f"Kubernetes namespace '{config.namespace}' does not exist on cluster"
            f" '{config.gke_cluster}'. Ensure XMC has been deployed first (e.g."
            " via deploy_dev_env.sh or INSTALL.md)."
        )

    log_success("All pre-flight checks and authentication steps passed successfully.")


def extract_migration_version(sql_files: list[Path]) -> int:
    """Extracts the maximum numeric integer version from SQL file paths.

    Args:
      sql_files: List of Path objects for SQL files.

    Returns:
      Highest numeric version integer found, or 0 if none.
    """
    return max(
        (int(p) for path in sql_files if (p := path.name.partition("_")[0]).isdigit()),
        default=0,
    )


def get_migration_status(
    config: DeploymentConfig, migrations_dir: str, spanner_instance: str, db_name: str
) -> tuple[int, int]:
    """Fetches local maximum SQL migration version and current database state.

    Args:
      config: Loaded DeploymentConfig instance.
      migrations_dir: Relative migrations directory name ('ess' or 'xid').
      spanner_instance: Target Spanner instance name.
      db_name: Target Spanner database name.

    Returns:
      Tuple of (current_version, max_version).
    """
    dir_path = Path(f"internal/storage/spanner/migrations/{migrations_dir}")
    sql_files = list(dir_path.glob("*.up.sql")) if dir_path.is_dir() else []
    max_version = extract_migration_version(sql_files)

    log_info(f"Fetching current {migrations_dir} database version...")
    cmd = [
        "bazel",
        "run",
        "//internal/storage/spanner/migrations:migrations",
        "--",
        f"--project={config.project_id}",
        f"--instance={spanner_instance}",
        f"--database={db_name}",
        f"--migrations_dir={migrations_dir}",
        "--command=version",
    ]
    res = run_command(cmd, check=False, capture_output=True)
    output = (res.stdout or "") + (res.stderr or "")

    dirty_match = re.search(r"Dirty:\s*(true|false)", output, re.IGNORECASE)
    dirty = dirty_match.group(1).lower() if dirty_match else "false"

    if dirty == "true":
        error_exit(
            f"{migrations_dir} database is in a DIRTY state. Please resolve"
            " manually before proceeding."
        )

    if res.returncode != 0:
        error_exit(
            f"Failed to query migration version for '{db_name}' (exit {res.returncode}):\n"
            f"{output.strip()}"
        )

    ver_match = re.search(r"Version:\s*([0-9]+)", output)
    current_version = int(ver_match.group(1)) if ver_match else 0

    return current_version, max_version


def run_migration(
    config: DeploymentConfig, migrations_dir: str, spanner_instance: str, db_name: str
) -> None:
    """Executes database schema migrations via Bazel command.

    Args:
      config: Loaded DeploymentConfig instance.
      migrations_dir: Migration directory name ('ess' or 'xid').
      spanner_instance: Target Spanner instance ID.
      db_name: Target Spanner database ID.
    """
    log_info(f"Running {migrations_dir} migrations...")
    cmd = [
        "bazel",
        "run",
        "//internal/storage/spanner/migrations:migrations",
        "--",
        f"--project={config.project_id}",
        f"--instance={spanner_instance}",
        f"--database={db_name}",
        f"--migrations_dir={migrations_dir}",
        "--command=up",
    ]
    run_command(cmd)


def check_pending_migrations(config: DeploymentConfig) -> list[tuple[str, str, str]]:
    """Checks ESS and XID databases for pending Spanner migrations.

    Args:
      config: Loaded DeploymentConfig instance.

    Returns:
      List of (migrations_dir, spanner_instance, db_name) tuples with pending migrations.
    """
    log_info("Checking for pending database migrations...")
    spanner_instance = f"xmc-{config.environment}"
    targets = [
        ("ess", f"experiment-db-{config.environment}"),
        ("xid", f"xids-db-{config.environment}"),
    ]
    pending: list[tuple[str, str, str]] = []
    for mig_dir, db_name in targets:
        current, available = get_migration_status(config, mig_dir, spanner_instance, db_name)
        log_info(f"{mig_dir.upper()} database version: current={current}, available={available}")
        if available > current:
            pending.append((mig_dir, spanner_instance, db_name))
    return pending


def apply_pending_migrations(config: DeploymentConfig, pending: list[tuple[str, str, str]]) -> None:
    """Applies pending Spanner database migrations.

    Args:
      config: Loaded DeploymentConfig instance.
      pending: List of (migrations_dir, spanner_instance, db_name) tuples to migrate.
    """
    for mig_dir, spanner_instance, db_name in pending:
        run_migration(config, mig_dir, spanner_instance, db_name)
    log_success("Migrations applied successfully.")


def step_migrate(config: UpgradeConfig) -> None:
    """Checks and applies pending Spanner database migrations."""
    pending = check_pending_migrations(config)
    if not pending:
        log_success("No pending migrations found. Databases are up to date.")
        return

    log_info("Pending migrations detected!")
    if not config.run_migrations:
        print("STATUS: PENDING_MIGRATIONS")
        sys.exit(10)

    log_info("Running pending migrations as requested...")
    apply_pending_migrations(config, pending)


def step_images(config: UpgradeConfig) -> None:
    """Rebuilds and pushes XMC microservice OCI images to Artifact Registry."""
    build_and_push_images(config)


def step_app(config: UpgradeConfig) -> None:
    """Upgrades XMC Kubernetes workloads via Cluster Toolkit gcluster CLI."""
    log_info("Upgrading XMC Kubernetes application workloads using Cluster Toolkit...")

    ctk_dir = Path(f"ctk-out/{config.deployment_name}")
    if not ctk_dir.is_dir():
        error_exit(
            f"Deployment directory '{ctk_dir}' not found. Ensure the environment"
            " has been deployed first."
        )

    log_info("Re-compiling XMC blueprint with latest variables and templates...")
    compile_blueprint(
        config.config_path, config.deployment_name, enable_components=config.enable_components
    )

    log_info("Exporting and importing Cluster Toolkit deployment group outputs...")
    if (ctk_dir / "primary").is_dir():
        run_command(["./cluster-toolkit/gcluster", "export-outputs", str(ctk_dir / "primary")])

    if (ctk_dir / "compute").is_dir():
        run_command(["./cluster-toolkit/gcluster", "import-inputs", str(ctk_dir / "compute")])
        run_command(["./cluster-toolkit/gcluster", "export-outputs", str(ctk_dir / "compute")])

    if (ctk_dir / "kubernetes_prereqs").is_dir():
        run_command(
            ["./cluster-toolkit/gcluster", "import-inputs", str(ctk_dir / "kubernetes_prereqs")]
        )

    log_info("Running gcluster deploy for group 'xmc_app'...")
    run_command(
        [
            "./cluster-toolkit/gcluster",
            "--download-dependencies",
            "deploy",
            str(ctk_dir),
            "--only",
            "xmc_app",
            "--auto-approve",
        ]
    )

    log_success("Application upgrade initiated.")
    deploy_component_profile(config)


def resolve_deployment_name(service: str, namespace: str) -> str | None:
    """Resolves target Kubernetes deployment name with suffix fallback.

    Args:
      service: Base service name (e.g. 'actuator').
      namespace: Kubernetes namespace.

    Returns:
      Resolved deployment name string or None if not present in cluster.
    """
    for candidate in [service, f"{service}-deployment"]:
        res = run_command(
            ["kubectl", "get", "deployment", candidate, "-n", namespace],
            check=False,
            capture_output=True,
        )
        if res.returncode == 0:
            return candidate
    return None


# Optional deployments (e.g. xmc-bridge, ingestion-gateway) are skipped by
# resolve_deployment_name when their feature flag leaves them undeployed.
# xmc-bridge is triggered first because it may own component releases (e.g.
# the ingestion-gateway workload). `rollout restart` is asynchronous, so the
# rollouts overlap; the bridge re-reconciles its releases once its new pod
# is up, so no rollout ordering is required.
_RESTART_SERVICES: tuple[str, ...] = (
    "xmc-bridge",
    "actuator",
    "dashboard-service",
    "experiment-state-server",
    "health-service",
    "ingestion-gateway",
    "log-monitor",
    "reconciler",
    "rod-data-provider",
    "ui",
    "xid-service",
)


def _restart_service(service: str, namespace: str) -> bool:
    """Restarts a single microservice deployment if present in the namespace."""
    resolved_name = resolve_deployment_name(service, namespace)
    if not resolved_name:
        log_info(
            f"Deployment '{service}' (or '{service}-deployment') not found in"
            f" namespace '{namespace}'. Skipping."
        )
        return False

    log_info(f"Restarting deployment/{resolved_name}...")
    run_command(
        [
            "kubectl",
            "rollout",
            "restart",
            f"deployment/{resolved_name}",
            "-n",
            namespace,
        ]
    )
    return True


def step_restart(config: DeploymentConfig) -> None:
    """Triggers rolling restarts for all microservices in Kubernetes."""
    log_info(
        "Triggering rolling restart across XMC service deployments to run newly"
        " pushed container images..."
    )
    restarted = sum(_restart_service(svc, config.namespace) for svc in _RESTART_SERVICES)
    if restarted == 0:
        error_exit(
            f"No XMC deployments resolved in namespace '{config.namespace}'."
            " Verify cluster credentials and workload deployment state."
        )
    log_success("Rolling deployment restarts initiated.")


def wait_for_deployments_ready(config: DeploymentConfig) -> None:
    """Waits for active service deployments in the namespace to finish rolling out."""
    log_info(
        f"Waiting for deployments in namespace '{config.namespace}' to complete rollout (3m timeout)..."
    )
    for svc in _RESTART_SERVICES:
        resolved = resolve_deployment_name(svc, config.namespace)
        if resolved:
            run_command(
                [
                    "kubectl",
                    "rollout",
                    "status",
                    f"deployment/{resolved}",
                    "-n",
                    config.namespace,
                    "--timeout=180s",
                ]
            )


def step_verify(config: UpgradeConfig) -> None:
    """Runs the environment health verification script."""
    log_info(
        f"Verifying rollout completion and cluster health in namespace '{config.namespace}'..."
    )
    wait_for_deployments_ready(config)
    log_info("Running verification script...")

    env = {
        "ENVIRONMENT": config.environment,
        "REGION": config.region,
        "PROJECT_ID": config.project_id,
        "ENABLE_COMPONENTS": "true" if config.enable_components else "false",
        "LOG_MONITOR_DEPLOYMENT_ENABLED": (
            "true" if config.log_monitor_deployment_enabled else "false"
        ),
    }

    run_command([sys.executable, "./deploy/ctk_v2/verify_xmc.py"], env=env)
    log_success("XMC environment upgrade to current repository branch state verified successfully.")


def execute_step(step_name: str, config: UpgradeConfig) -> None:
    """Executes a single step by name.

    Args:
      step_name: Name of the step to execute.
      config: Active UpgradeConfig object.
    """
    step_map = {
        "check": step_check,
        "migrate": step_migrate,
        "images": step_images,
        "app": step_app,
        "restart": step_restart,
        "verify": step_verify,
    }
    step_fn = step_map[step_name]
    step_fn(config)


def run_pipeline(args: argparse.Namespace, config: UpgradeConfig, steps: list[str]) -> None:
    """Executes the upgrade pipeline according to --only or --start-from.

    Args:
      args: Parsed argparse.Namespace.
      config: Loaded UpgradeConfig instance.
      steps: Ordered list of pipeline step names.
    """
    if args.only:
        try:
            execute_step(args.only, config)
        except subprocess.CalledProcessError as e:
            print(f"\n{RED}Error executing step '{args.only}': {e}{NC}")
            print(
                "Suggested recovery command:\n  python3 tools/dev_env/upgrade_dev_env.py"
                f" --config {config.config_path} --start-from {args.only}"
            )
            sys.exit(1)
        return

    should_run = not bool(args.start_from)
    for step in steps:
        if step == args.start_from:
            should_run = True
        if should_run:
            try:
                execute_step(step, config)
            except subprocess.CalledProcessError as e:
                print(f"\n{RED}Error executing step '{step}': {e}{NC}")
                print(
                    "Suggested recovery command:\n  python3 tools/dev_env/upgrade_dev_env.py"
                    f" --config {config.config_path} --start-from {step}"
                )
                sys.exit(1)

    log_success("Upgrade completed end-to-end successfully!")


def run_upgrade(args: argparse.Namespace) -> None:
    """Executes the upgrade pipeline.

    Args:
        args: Parsed command-line arguments.
    """
    config = load_config(args.config, run_migrations=args.run_migrations)
    if getattr(args, "enable_components", None) is not None:
        config = replace(config, enable_components=args.enable_components)
    if getattr(args, "profile_values", None):
        config = replace(config, profile_values=args.profile_values)
    run_pipeline(args, config, STEPS)


def main(args_list: list[str] | None = None) -> None:
    """Main entrypoint for the upgrade script."""
    ensure_venv()
    repo_root = Path(__file__).resolve().parent.parent.parent
    if (repo_root / "WORKSPACE").exists() or (repo_root / "MODULE.bazel").exists():
        os.chdir(repo_root)
    elif (Path(__file__).resolve().parent.parent / "WORKSPACE").exists() or (
        Path(__file__).resolve().parent.parent / "MODULE.bazel"
    ).exists():
        os.chdir(Path(__file__).resolve().parent.parent)

    args = parse_args(args_list)
    run_upgrade(args)


if __name__ == "__main__":
    main()
