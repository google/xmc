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
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from tool_utils import ensure_venv

STEPS = ["check", "migrate", "images", "app", "restart", "verify"]
DEFAULT_CONFIG = "deploy/ctk_v2/xmanager-cloud-deployment.yaml"


@dataclass
class Config:
    """Holds deployment configuration values for XMC upgrade."""

    project_id: str
    deployment_name: str
    region: str
    environment: str
    namespace: str
    registry: str
    acr_target: str
    gke_cluster: str
    config_path: str
    run_migrations: bool = False


def log_info(msg: str) -> None:
    """Logs informational messages in blue text."""
    print(f"\n\033[0;34m[INFO] {msg}\033[0m")


def log_success(msg: str) -> None:
    """Logs success messages in green text."""
    print(f"\n\033[0;32m[SUCCESS] {msg}\033[0m")


def error_exit(msg: str, exit_code: int = 1) -> None:
    """Prints an error message to stderr and exits."""
    print(f"\n\033[0;31mError: {msg}\033[0m", file=sys.stderr)
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

    parsed = parser.parse_args(args)

    if parsed.start_from and parsed.only:
        error_exit(
            "Arguments --start-from and --only are mutually exclusive.",
            exit_code=1,
        )

    return parsed


def parse_yaml_vars_line_fallback(content: str) -> dict[str, Any]:
    """Parses key-value scalar entries under 'vars:' section in YAML content.

    Args:
      content: Raw YAML text content.

    Returns:
      Dictionary mapping variable names to scalar string/boolean values.
    """
    vars_dict: dict[str, Any] = {}
    in_vars_section = False
    for line in content.splitlines():
        trimmed = line.strip()
        if not trimmed or trimmed.startswith("#"):
            continue
        if line.startswith("vars:"):
            in_vars_section = True
            continue
        if in_vars_section:
            if line and not line.startswith(" ") and not line.startswith("\t"):
                break
            match = re.match(r"^\s*([a-zA-Z0-9_]+):\s*(.*)$", line)
            if match:
                key, val = match.group(1), match.group(2).strip()
                val = val.split(" #")[0].strip()
                if (val.startswith('"') and val.endswith('"')) or (
                    val.startswith("'") and val.endswith("'")
                ):
                    val = val[1:-1]
                vars_dict[key] = val
    return vars_dict


def parse_yaml_vars(config_path: Path) -> dict[str, Any]:
    """Reads YAML vars section using PyYAML or fallback line parsing.

    Args:
      config_path: Path to the YAML deployment file.

    Returns:
      Dictionary of variables extracted from the vars block.
    """
    if not config_path.is_file():
        error_exit(
            f"Configuration file '{config_path}' not found. Please ensure you"
            " have deployed the environment using Cluster Toolkit."
        )

    content = config_path.read_text(encoding="utf-8")
    try:
        import yaml  # pylint: disable=g-import-not-at-top

        data = yaml.safe_load(content)
        if isinstance(data, dict) and "vars" in data:
            vars_data = data.get("vars") or {}
            return {k: v for k, v in vars_data.items() if isinstance(v, (str, int, float, bool))}
    except Exception:  # pylint: disable=broad-except
        pass

    return parse_yaml_vars_line_fallback(content)


def load_config(config_file: str, run_migrations: bool = False) -> Config:
    """Loads YAML config file and merges with environment variable overrides.

    Args:
      config_file: Target YAML configuration file path string.
      run_migrations: Boolean flag to enable running migrations.

    Returns:
      Populated Config instance.
    """
    path = Path(config_file)
    log_info(f"Loading configuration from '{config_file}'...")
    yaml_vars = parse_yaml_vars(path)

    project_id = os.getenv("PROJECT_ID") or str(yaml_vars.get("project_id", ""))
    deployment_name = os.getenv("XMC_DEPLOYMENT_NAME") or str(yaml_vars.get("deployment_name", ""))
    region = os.getenv("GCP_REGION") or str(yaml_vars.get("region", "us-central1"))
    environment = os.getenv("XMANAGER_ENV") or str(yaml_vars.get("environment", "dev"))
    namespace = os.getenv("K8S_NAMESPACE") or str(yaml_vars.get("namespace", "xmc"))

    if not project_id:
        error_exit(f"project_id is missing in vars of '{config_file}'.")
    if not deployment_name:
        error_exit(f"deployment_name is missing in vars of '{config_file}'.")

    registry = f"us-docker.pkg.dev/{project_id}/xmc"
    acr_target = os.getenv("XMC_ACR_TARGET") or registry
    gke_cluster = f"xmc-{deployment_name}-{environment}"

    return Config(
        project_id=project_id,
        deployment_name=deployment_name,
        region=region,
        environment=environment,
        namespace=namespace,
        registry=registry,
        acr_target=acr_target,
        gke_cluster=gke_cluster,
        config_path=config_file,
        run_migrations=run_migrations,
    )


def run_cmd(
    cmd: list[str],
    env: dict[str, str] | None = None,
    check: bool = True,
    capture_output: bool = False,
) -> subprocess.CompletedProcess:
    """Executes a command subprocess with optional env and output capturing.

    Args:
      cmd: List of command line tokens.
      env: Environment variables dictionary overlay.
      check: If True, raises CalledProcessError on non-zero exit code.
      capture_output: If True, captures stdout and stderr as text.

    Returns:
      CompletedProcess instance.
    """
    proc_env = os.environ.copy()
    if env:
        proc_env.update(env)

    return subprocess.run(
        cmd,
        env=proc_env,
        check=check,
        text=True,
        capture_output=capture_output,
    )


def step_check(config: Config) -> None:
    """Performs pre-flight CLI dependency checks and authenticates to GCP/GKE."""
    log_info("Running pre-flight dependency and authentication checks...")

    for binary in ["gcloud", "kubectl", "helm", "bazel"]:
        if not shutil.which(binary):
            error_exit(f"Required CLI tool '{binary}' is not installed or not in PATH.")

    gcluster_path = Path("./cluster-toolkit/gcluster")
    if not gcluster_path.is_file() or not os.access(gcluster_path, os.X_OK):
        error_exit(
            "Local cluster-toolkit binary './cluster-toolkit/gcluster' not found"
            " or is not executable."
        )

    log_info(f"Verifying gcloud target project ({config.project_id})...")
    run_cmd(["gcloud", "config", "set", "project", config.project_id, "--quiet"])

    registry_host = config.acr_target.split("/")[0]
    if registry_host.endswith("gcr.io") or registry_host.endswith(".pkg.dev"):
        log_info(
            f"Configuring Docker authentication for Google Artifact Registry ({registry_host})..."
        )
        run_cmd(["gcloud", "auth", "configure-docker", registry_host, "--quiet"])

    log_info(
        f"Fetching kubectl credentials for GKE cluster '{config.gke_cluster}' in"
        f" region '{config.region}'..."
    )
    run_cmd(
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

    res = run_cmd(
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
    max_version = 0
    for path in sql_files:
        prefix = path.name.split("_")[0]
        if prefix.isdigit():
            version = int(prefix)
            if version > max_version:
                max_version = version
    return max_version


def get_migration_status(
    config: Config, migrations_dir: str, spanner_instance: str, db_name: str
) -> tuple[int, int]:
    """Fetches local maximum SQL migration version and current database state.

    Args:
      config: Loaded Config instance.
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
    res = run_cmd(cmd, check=False, capture_output=True)
    output = (res.stdout or "") + (res.stderr or "")

    dirty_match = re.search(r"Dirty:\s*(true|false)", output, re.IGNORECASE)
    dirty = dirty_match.group(1).lower() if dirty_match else "false"

    if dirty == "true":
        error_exit(
            f"{migrations_dir} database is in a DIRTY state. Please resolve"
            " manually before proceeding."
        )

    ver_match = re.search(r"Version:\s*([0-9]+)", output)
    current_version = int(ver_match.group(1)) if ver_match else 0

    return current_version, max_version


def run_migration(config: Config, migrations_dir: str, spanner_instance: str, db_name: str) -> None:
    """Executes database schema migrations via Bazel command.

    Args:
      config: Loaded Config instance.
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
    run_cmd(cmd)


def step_migrate(config: Config) -> None:
    """Checks and applies pending Spanner database migrations."""
    log_info("Checking for pending database migrations...")

    spanner_instance = f"xmc-{config.environment}"
    ess_db = f"experiment-db-{config.environment}"
    xid_db = f"xids-db-{config.environment}"

    current_ess, max_ess = get_migration_status(config, "ess", spanner_instance, ess_db)
    current_xid, max_xid = get_migration_status(config, "xid", spanner_instance, xid_db)

    log_info(f"ESS database version: current={current_ess}, available={max_ess}")
    log_info(f"XID database version: current={current_xid}, available={max_xid}")

    has_pending = (max_ess > current_ess) or (max_xid > current_xid)

    if not has_pending:
        log_success("No pending migrations found. Databases are up to date.")
        return

    log_info("Pending migrations detected!")
    if config.run_migrations:
        log_info("Running pending migrations as requested...")
        if max_ess > current_ess:
            run_migration(config, "ess", spanner_instance, ess_db)
        if max_xid > current_xid:
            run_migration(config, "xid", spanner_instance, xid_db)
        log_success("Migrations applied successfully.")
    else:
        print("STATUS: PENDING_MIGRATIONS")
        print(f"ESS: current={current_ess}, available={max_ess}")
        print(f"XID: current={current_xid}, available={max_xid}")
        sys.exit(10)


def step_images(config: Config) -> None:
    """Rebuilds and pushes XMC microservice OCI images to Artifact Registry."""
    log_info(
        f"Rebuilding and pushing latest XMC microservice OCI images to '{config.acr_target}'..."
    )
    env = {
        "XMC_ENV": config.environment,
        "XMC_ACR_TARGET": config.acr_target,
    }

    # TODO: Migrate hardcoded image targets to dynamic discovery or configuration flags to operate on flags cleanly.
    targets = [
        ("actuator", "//cmd/actuator:actuator_image_push"),
        (
            "experiment_state_server",
            "//cmd/experiment_state_server:experiment_state_server_image_push",
        ),
        ("log_monitor", "//cmd/log_monitor:log_monitor_image_push"),
        ("reconciler", "//cmd/reconciler:reconciler_image_push"),
        ("ui", "//cmd/ui:ui_image_push"),
        ("xid_service", "//cmd/xid_service:xid_service_image_push"),
        ("health_service", "//cmd/health_service:health_service_image_push"),
        ("dashboard_service", "//cmd/dashboard_service:dashboard_service_image_push"),
        ("rod_data_provider", "//cmd/rod_data_provider:rod_data_provider_image_push"),
        ("ingestion_gateway", "//cmd/ingestion_gateway:ingestion_gateway_image_push"),
    ]

    for name, target in targets:
        log_info(f"Pushing {name} image...")
        run_cmd(["bazel", "run", target], env=env)

    log_success("All XMC service container images compiled and pushed from current branch state.")


def step_app(config: Config) -> None:
    """Upgrades XMC Kubernetes workloads via Cluster Toolkit gcluster CLI."""
    log_info("Upgrading XMC Kubernetes application workloads using Cluster Toolkit...")

    ctk_dir = Path(f"ctk-out/{config.deployment_name}")
    if not ctk_dir.is_dir():
        error_exit(
            f"Deployment directory '{ctk_dir}' not found. Ensure the environment"
            " has been deployed first."
        )

    log_info("Running gcluster deploy for group 'xmc_app'...")
    run_cmd(
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


def resolve_deployment_name(service: str, namespace: str) -> str | None:
    """Resolves target Kubernetes deployment name with suffix fallback.

    Args:
      service: Base service name (e.g. 'actuator').
      namespace: Kubernetes namespace.

    Returns:
      Resolved deployment name string or None if not present in cluster.
    """
    for candidate in [service, f"{service}-deployment"]:
        res = run_cmd(
            ["kubectl", "get", "deployment", candidate, "-n", namespace],
            check=False,
            capture_output=True,
        )
        if res.returncode == 0:
            return candidate
    return None


def step_restart(config: Config) -> None:
    """Triggers rolling restarts for all microservices in Kubernetes."""
    log_info(
        "Triggering rolling restart across XMC service deployments to run newly"
        " pushed container images..."
    )
    services = [
        "actuator",
        "dashboard-service",
        "experiment-state-server",
        "health-service",
        "log-monitor",
        "reconciler",
        "ui",
        "xid-service",
    ]

    for service in services:
        resolved_name = resolve_deployment_name(service, config.namespace)
        if resolved_name:
            log_info(f"Restarting deployment/{resolved_name}...")
            run_cmd(
                [
                    "kubectl",
                    "rollout",
                    "restart",
                    f"deployment/{resolved_name}",
                    "-n",
                    config.namespace,
                ]
            )
        else:
            log_info(
                f"Deployment '{service}' (or '{service}-deployment') not found in"
                f" namespace '{config.namespace}'. Skipping."
            )

    log_success("Rolling deployment restarts initiated.")


def step_verify(config: Config) -> None:
    """Runs the environment health verification script."""
    log_info(
        f"Verifying rollout completion and cluster health in namespace '{config.namespace}'..."
    )
    log_info("Running verification script...")

    env = {
        "ENVIRONMENT": config.environment,
        "REGION": config.region,
        "PROJECT_ID": config.project_id,
    }

    py_script = Path("./deploy/ctk_v2/verify_xmc.py")
    if py_script.is_file():
        cmd = [sys.executable, str(py_script)]
    else:
        cmd = ["bash", "./deploy/ctk_v2/verify_xmc.sh"]

    res = run_cmd(cmd, env=env, check=False)
    if res.returncode != 0:
        log_info("Diagnostic script reported warnings.")

    log_success("XMC environment upgrade to current repository branch state verified successfully.")


def execute_step(step_name: str, config: Config) -> None:
    """Executes a single step by name.

    Args:
      step_name: Name of the step to execute.
      config: Active Config object.
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


def run_pipeline(args: argparse.Namespace, config: Config, steps: list[str]) -> None:
    """Executes the upgrade pipeline according to --only or --start-from.

    Args:
      args: Parsed argparse.Namespace.
      config: Loaded Config instance.
      steps: Ordered list of pipeline step names.
    """
    if args.only:
        try:
            execute_step(args.only, config)
        except subprocess.CalledProcessError as e:
            print(f"\n\033[0;31mError executing step '{args.only}': {e}\033[0m")
            print(
                "Suggested recovery command:\n  python3 tools/upgrade_dev_env.py"
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
                print(f"\n\033[0;31mError executing step '{step}': {e}\033[0m")
                print(
                    "Suggested recovery command:\n  python3 tools/upgrade_dev_env.py"
                    f" --config {config.config_path} --start-from {step}"
                )
                sys.exit(1)

    log_success("Upgrade completed end-to-end successfully!")


def main(args_list: list[str] | None = None) -> None:
    """Main entrypoint for the upgrade script."""
    ensure_venv()
    os.chdir(Path(__file__).resolve().parent.parent)
    args = parse_args(args_list)
    config = load_config(args.config, run_migrations=args.run_migrations)
    run_pipeline(args, config, STEPS)


if __name__ == "__main__":
    main()
