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

"""XMC v2 Dev Environment Deployment Script."""

import argparse
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import time
from typing import Any, NoReturn

from tool_utils import ensure_venv

# ANSI Color Codes
RED = "\033[0;31m"
GREEN = "\033[0;32m"
BLUE = "\033[0;34m"
NC = "\033[0m"  # No Color
BOLD = "\033[1m"


def log_info(msg: str) -> None:
    """Logs an informational message to stdout.

    Args:
        msg: The message to log.
    """
    print(f"\n{BLUE}[INFO] {msg}{NC}")


def log_success(msg: str) -> None:
    """Logs a success message to stdout.

    Args:
        msg: The message to log.
    """
    print(f"\n{GREEN}[SUCCESS] {msg}{NC}")


def log_error(msg: str) -> None:
    """Logs an error message to stderr.

    Args:
        msg: The message to log.
    """
    print(f"\n{RED}[ERROR] {msg}{NC}", file=sys.stderr)


def error_exit(msg: str) -> NoReturn:
    """Logs an error message and exits with status 1.

    Args:
        msg: The error message to log.
    """
    log_error(msg)
    sys.exit(1)


# Allowed steps
STEPS = [
    "check",
    "compile",
    "images",
    "apis",
    "primary",
    "compute",
    "gateway-api",
    "prereqs",
    "app",
    "verify",
]

# Config regex patterns
CONFIG_VALIDATION_SCHEMAS = {
    "deployment_name": r"^[a-zA-Z0-9_-]+$",
    "project_id": r"^[a-z0-9:.-]+$",
    "environment": r"^[a-zA-Z0-9_-]+$",
    "region": r"^[a-z0-9-]+$",
    "domain_name": r"^[a-zA-Z0-9.-]+$",
    "image_registry": r"^[a-zA-Z0-9./:-]+$",
    "image_tag": r"^[a-zA-Z0-9_.-]+$",
}


def parse_args(args: list[str]) -> argparse.Namespace:
    """Parses command line arguments.

    Args:
        args: A list of command line arguments to parse.

    Returns:
        An argparse.Namespace object containing the parsed arguments.
    """
    parser = argparse.ArgumentParser(description="XMC v2 Dev Environment Deployment Script")
    parser.add_argument(
        "--config",
        "-c",
        required=True,
        help="Path to XMC deployment YAML configuration file (Required)",
    )
    parser.add_argument(
        "--dns-project",
        required=True,
        help="GCP Project ID hosting the DNS zone (Required)",
    )
    parser.add_argument("--dns-zone", required=True, help="Cloud DNS Managed Zone name (Required)")
    parser.add_argument(
        "--start-from",
        "-s",
        choices=STEPS,
        help="Start/resume deployment from this step onwards",
    )
    parser.add_argument("--only", choices=STEPS, help="Execute ONLY this specific step and exit")

    parsed_args = parser.parse_args(args)

    if parsed_args.start_from and parsed_args.only:
        parser.error("Cannot specify both --start-from and --only.")

    return parsed_args


def _load_config(config_path: str) -> dict[str, str]:
    """Loads the configuration file using a custom parser.

    This avoids a dependency on PyYAML. It handles simple key-value pairs,
    ignores comments, and correctly handles quoted values that may contain
    hash characters.

    Args:
        config_path: Path to the YAML configuration file.

    Returns:
        A dictionary containing the parsed configuration keys and values.
    """
    path = pathlib.Path(config_path)
    if not path.exists():
        error_exit(f"Configuration file not found at '{config_path}'.")

    config = {}
    try:
        with open(config_path) as f:
            for line in f:
                line_stripped = line.strip()
                if not line_stripped or line_stripped.startswith("#"):
                    continue

                if ":" not in line:
                    continue

                parts = line.split(":", 1)
                k = parts[0].strip()
                v = parts[1].strip()

                # Handle quoted values
                if (v.startswith('"') and v.count('"') >= 2) or (
                    v.startswith("'") and v.count("'") >= 2
                ):
                    quote_char = v[0]
                    end_quote_idx = v.find(quote_char, 1)
                    if end_quote_idx != -1:
                        v = v[1:end_quote_idx]
                else:
                    # Unquoted value, strip trailing comments
                    if "#" in v:
                        v = v.split("#", 1)[0].strip()

                # First match wins (similar to bash grep | head -n 1)
                if k not in config:
                    config[k] = v
    except Exception as e:
        error_exit(f"Failed to read configuration file: {e}")

    if not config:
        error_exit("Configuration file is empty or invalid.")

    return config


def _validate_config(config: dict[str, str], config_path: str) -> dict[str, str]:
    """Validates the configuration dict against schemas.

    Args:
        config: The configuration dictionary to validate.
        config_path: Path to the configuration file (used for error messages).

    Returns:
        A dictionary containing only the validated configuration keys.
    """
    validated_config: dict[str, str] = {}
    for key, pattern_str in CONFIG_VALIDATION_SCHEMAS.items():
        val = config.get(key)
        if val is None:
            error_exit(f"Required configuration field '{key}' is missing from '{config_path}'.")

        if not re.match(pattern_str, val):
            error_exit(f"Field '{key}' value '{val}' is invalid. Must match regex: {pattern_str}")
        validated_config[key] = val

    return validated_config


def load_and_validate_config(config_path: str) -> dict[str, str]:
    """Loads and validates the configuration file.

    Args:
        config_path: Path to the configuration file.

    Returns:
        A dictionary containing the validated configuration.
    """
    config = _load_config(config_path)
    return _validate_config(config, config_path)


def run_command(
    cmd: list[str],
    env: dict[str, str] | None = None,
    capture_output: bool = False,
    log_on_error: bool = True,
) -> subprocess.CompletedProcess:
    """Helper to run shell commands.

    Args:
        cmd: The command to run as a list of strings.
        env: Optional environment variables to merge with current env.
        capture_output: Whether to capture stdout/stderr.
        log_on_error: Whether to log errors to stderr on failure.

    Returns:
        A subprocess.CompletedProcess object.

    Raises:
        subprocess.CalledProcessError: If the command fails.
    """
    log_info(f"Running: {' '.join(cmd)}")
    try:
        # Merge current env with provided env
        run_env = os.environ.copy()
        if env:
            run_env.update(env)

        return subprocess.run(
            cmd,
            env=run_env,
            check=True,
            text=True,
            capture_output=capture_output,
        )
    except subprocess.CalledProcessError as e:
        if log_on_error:
            log_error(f"Command failed: {' '.join(cmd)}")
            if e.stdout:
                log_error(f"Captured Stdout:\n{e.stdout}")
            if e.stderr:
                log_error(f"Captured Stderr:\n{e.stderr}")
        # Re-raise to be caught by the orchestrator for error reporting
        raise e


def parse_version(v: str) -> tuple[int, ...]:
    """Parses a version string into a tuple of integers.

    Args:
        v: The version string (e.g., "1.12.2").

    Returns:
        A tuple of integers representing the version.
    """
    return tuple(map(int, re.findall(r"\d+", v)[:3]))


def _verify_binaries() -> None:
    """Verifies required CLI binaries are installed and gcluster is executable."""
    required_binaries = [
        "python3",
        "gcloud",
        "kubectl",
        "helm",
        "terraform",
        "bazel",
    ]
    for binary in required_binaries:
        if not shutil.which(binary):
            error_exit(f"CLI dependency '{binary}' is required but not installed.")

    local_gcluster = pathlib.Path("./cluster-toolkit/gcluster")
    if not local_gcluster.is_file() or not os.access(local_gcluster, os.X_OK):
        error_exit(
            f"Local cluster-toolkit binary '{local_gcluster}' not found or is not executable."
        )


def _check_terraform_version() -> None:
    """Checks if Terraform version is >= 1.12.2."""
    try:
        tf_version_out = run_command(["terraform", "version", "-json"], capture_output=True).stdout
        tf_version_data = json.loads(tf_version_out)
        tf_version = tf_version_data.get("terraform_version")
        if not tf_version:
            error_exit("Could not detect Terraform version from JSON output.")

        if parse_version(tf_version) < parse_version("1.12.2"):
            error_exit(f"Terraform version must be >= 1.12.2. Found: {tf_version}")
    except Exception as e:
        error_exit(f"Failed to check Terraform version: {e}")


def _check_gcp_project_alignment(config_project_id: str) -> None:
    """Checks if active gcloud project matches config project.

    Args:
        config_project_id: The project ID expected from configuration.
    """
    try:
        active_project = run_command(
            ["gcloud", "config", "get-value", "project"], capture_output=True
        ).stdout.strip()
        if active_project != config_project_id:
            error_exit(
                f"Active gcloud project ('{active_project}') does not match config"
                f" project ('{config_project_id}'). Please switch project first."
            )
    except subprocess.CalledProcessError as e:
        error_exit(f"Failed to get active gcloud project: {e}")


def _check_dns_access(dns_project: str, dns_zone: str) -> None:
    """Verifies access to Cloud DNS project and zone.

    Args:
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info(f"Verifying access to Cloud DNS project '{dns_project}' and zone '{dns_zone}'...")
    try:
        run_command(
            [
                "gcloud",
                "dns",
                "managed-zones",
                "describe",
                dns_zone,
                f"--project={dns_project}",
            ],
            capture_output=True,
        )
    except subprocess.CalledProcessError:
        error_exit(
            f"Could not describe Cloud DNS zone '{dns_zone}' in project"
            f" '{dns_project}'. Check your permissions."
        )


def run_checks(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Performs pre-flight checks.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Running pre-flight dependency checks...")
    _verify_binaries()
    _check_terraform_version()
    _check_gcp_project_alignment(config["project_id"])
    _check_dns_access(dns_project, dns_zone)
    log_success("All pre-flight checks passed.")


def _dns_transaction_abort(dns_project: str, dns_zone: str) -> None:
    """Aborts any active DNS transaction.

    Args:
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    try:
        run_command(
            [
                "gcloud",
                "dns",
                "record-sets",
                "transaction",
                "abort",
                f"--zone={dns_zone}",
                f"--project={dns_project}",
            ],
            capture_output=True,
            log_on_error=False,
        )
    except subprocess.CalledProcessError:
        pass  # Ignore if no transaction was active


def _dns_transaction_start(dns_project: str, dns_zone: str) -> None:
    """Starts a new DNS transaction.

    Args:
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    run_command(
        [
            "gcloud",
            "dns",
            "record-sets",
            "transaction",
            "start",
            f"--zone={dns_zone}",
            f"--project={dns_project}",
        ]
    )


def _dns_transaction_execute(dns_project: str, dns_zone: str) -> None:
    """Executes the active DNS transaction.

    Args:
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    run_command(
        [
            "gcloud",
            "dns",
            "record-sets",
            "transaction",
            "execute",
            f"--zone={dns_zone}",
            f"--project={dns_project}",
        ]
    )


def _dns_get_existing_ip(subdomain: str, dns_project: str, dns_zone: str) -> str:
    """Gets the existing IP for a subdomain.

    Args:
        subdomain: The subdomain to check (must end with a dot).
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.

    Returns:
        The existing IP address as a string, or empty string if not found.
    """
    return run_command(
        [
            "gcloud",
            "dns",
            "record-sets",
            "list",
            f"--zone={dns_zone}",
            f"--project={dns_project}",
            f"--name={subdomain}",
            "--type=A",
            "--format=value(rrdatas)",
        ],
        capture_output=True,
    ).stdout.strip()


def _dns_add_record(subdomain: str, ip: str, dns_project: str, dns_zone: str) -> None:
    """Adds a record to the active DNS transaction.

    Args:
        subdomain: The subdomain for the record (must end with a dot).
        ip: The target IP address.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info(f"Creating new DNS record: {subdomain} -> {ip}")
    run_command(
        [
            "gcloud",
            "dns",
            "record-sets",
            "transaction",
            "add",
            ip,
            f"--name={subdomain}",
            "--ttl=300",
            "--type=A",
            f"--zone={dns_zone}",
            f"--project={dns_project}",
        ]
    )


def apply_dns_records(ip: str, config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Automates DNS record configuration.

    Args:
        ip: The target IP address for the DNS records.
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info(f"Verifying Cloud DNS record states in project '{dns_project}', zone '{dns_zone}'...")

    # End domains with dot for DNS A-records
    d_app = f"{config['environment']}.{config['domain_name']}."
    d_api = f"api.{config['environment']}.{config['domain_name']}."
    d_grpc = f"grpc.{config['environment']}.{config['domain_name']}."

    subdomains = [d_app, d_api, d_grpc]

    _dns_transaction_abort(dns_project, dns_zone)
    _dns_transaction_start(dns_project, dns_zone)

    has_transaction_changes = False
    transaction_aborted = False

    try:
        for subdomain in subdomains:
            existing_ip = _dns_get_existing_ip(subdomain, dns_project, dns_zone)

            if existing_ip == ip:
                log_info(f"DNS record for '{subdomain}' already points to {ip}. Skipping.")
            elif existing_ip:
                _dns_transaction_abort(dns_project, dns_zone)
                transaction_aborted = True
                error_exit(
                    f"DNS record for '{subdomain}' already exists but points to a"
                    f" different IP ({existing_ip}). Aborting to prevent accidental"
                    " conflict. Please delete it manually first if you want to update"
                    f" it to {ip}."
                )
            else:
                _dns_add_record(subdomain, ip, dns_project, dns_zone)
                has_transaction_changes = True

        if has_transaction_changes:
            _dns_transaction_execute(dns_project, dns_zone)
            log_success("Cloud DNS records successfully updated.")
        else:
            _dns_transaction_abort(dns_project, dns_zone)
            transaction_aborted = True
            log_success("Cloud DNS records are already up to date.")

    except Exception as e:
        if not transaction_aborted:
            _dns_transaction_abort(dns_project, dns_zone)
        raise e


def run_gcluster_deploy(deployment_name: str, group: str) -> None:
    """Runs gcluster deploy for a specific group.

    Args:
        deployment_name: The name of the deployment.
        group: The deployment group to deploy.
    """
    run_command(
        [
            "./cluster-toolkit/gcluster",
            "deploy",
            f"ctk-out/{deployment_name}",
            "--only",
            group,
            "--auto-approve",
        ]
    )


# --- Steps implementation ---


def step_check(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Verifies pre-flight checks (noop step for status check).

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_success("Pre-flight checks verified.")


def step_compile(
    config: dict[str, Any],
    dns_project: str,
    dns_zone: str,
    config_path: str,
) -> None:
    """Compiles the XMC blueprint using gcluster.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
        config_path: Path to the configuration file.
    """
    log_info("Compiling XMC blueprint using gcluster...")
    run_command(
        [
            "./cluster-toolkit/gcluster",
            "create",
            "deploy/ctk_v2/xmanager-cloud.yaml",
            "-d",
            config_path,
            "-o",
            "ctk-out",
            "-l",
            "WARNING",
            "-w",
            "--force",
        ]
    )
    log_success("Blueprint compiled successfully under 'ctk-out/'.")


def step_apis(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Deploys the APIs group (API enablement).

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Deploying apis group (API enablement)...")
    run_gcluster_deploy(config["deployment_name"], "apis")
    log_success("APIs enabled successfully.")


def step_primary(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Deploys the primary group (Network, Databases & certs) and configures DNS.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.

    Raises:
        RuntimeError: If Gateway IP extraction fails.
    """
    log_info("Deploying primary group (Network, Databases & certs)...")
    run_gcluster_deploy(config["deployment_name"], "primary")

    gw_ip_name = f"xmc-gateway-static-ip-{config['environment']}"
    log_info(f"Extracting static Gateway IP ({gw_ip_name})...")
    try:
        gateway_ip = run_command(
            [
                "gcloud",
                "compute",
                "addresses",
                "describe",
                gw_ip_name,
                "--global",
                f"--project={config['project_id']}",
                "--format=value(address)",
            ],
            capture_output=True,
        ).stdout.strip()
    except subprocess.CalledProcessError as e:
        raise RuntimeError(f"Failed to extract gateway IP address: {e}") from e

    if not gateway_ip:
        raise RuntimeError("Failed to extract gateway IP address (empty output).")

    log_success(f"Core Infrastructure deployed. Gateway IP: {gateway_ip}")
    apply_dns_records(gateway_ip, config, dns_project, dns_zone)


def step_compute(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Deploys the compute group (GKE Cluster and Node pools).

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Deploying compute group (GKE Cluster and Node pools)...")
    run_gcluster_deploy(config["deployment_name"], "compute")
    log_success("Compute group (GKE cluster and Node pools) deployed successfully.")


def step_gateway_api(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Enables GKE Gateway API on the cluster if not already enabled.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.

    Raises:
        RuntimeError: If cluster description fails.
    """
    cluster_name = f"xmc-{config['deployment_name']}-{config['environment']}"
    log_info(f"Checking if GKE Gateway API is enabled on cluster '{cluster_name}'...")

    try:
        gw_enabled = run_command(
            [
                "gcloud",
                "container",
                "clusters",
                "describe",
                cluster_name,
                f"--region={config['region']}",
                f"--project={config['project_id']}",
                "--format=value(gatewayConfig.enabled)",
            ],
            capture_output=True,
        ).stdout.strip()
    except subprocess.CalledProcessError as e:
        raise RuntimeError(f"Failed to describe cluster: {e}") from e

    if gw_enabled.lower() != "true":
        log_info("Enabling GKE Gateway API (standard channel)...")
        run_command(
            [
                "gcloud",
                "container",
                "clusters",
                "update",
                cluster_name,
                "--gateway-api=standard",
                f"--region={config['region']}",
                f"--project={config['project_id']}",
            ]
        )
        log_success("Gateway API enabled.")
    else:
        log_info("Gateway API standard channel is already enabled.")


def step_prereqs(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Deploys kubernetes_prereqs group (cert-manager, Kueue, Jobset).

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Deploying kubernetes_prereqs group (cert-manager, Kueue, Jobset)...")
    run_gcluster_deploy(config["deployment_name"], "kubernetes_prereqs")
    log_success("Kubernetes prerequisites (cert-manager, Kueue, Jobset) deployed successfully.")


def step_images(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Builds and pushes XMC service images.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Configuring Docker registry authorization for registry...")
    acr_target = config["image_registry"]
    registry_host = acr_target.split("/")[0]

    if registry_host.endswith("gcr.io") or registry_host.endswith(".pkg.dev"):
        run_command(["gcloud", "auth", "configure-docker", registry_host, "--quiet"])
    else:
        log_info(f"Non-GCP registry detected ({registry_host}). Skipping gcloud credential helper.")

    log_info(f"Building and pushing XMC Service Images using registry {acr_target}...")

    env_vars = {
        "XMC_ENV": config["environment"],
        "XMC_ACR_TARGET": acr_target,
        "BUILD_TAG": config["image_tag"],
    }

    # TODO: Migrate hardcoded image targets to dynamic discovery or configuration flags to operate on flags cleanly.
    bazel_targets = [
        "//cmd/actuator:actuator_image_push",
        "//cmd/experiment_state_server:experiment_state_server_image_push",
        "//cmd/log_monitor:log_monitor_image_push",
        "//cmd/reconciler:reconciler_image_push",
        "//cmd/ui:ui_image_push",
        "//cmd/xid_service:xid_service_image_push",
        "//cmd/health_service:health_service_image_push",
        "//cmd/dashboard_service:dashboard_service_image_push",
        "//cmd/rod_data_provider:rod_data_provider_image_push",
        "//cmd/ingestion_gateway:ingestion_gateway_image_push",
    ]

    for target in bazel_targets:
        log_info(f"Pushing {target.split(':')[-1]}...")
        run_command(["bazel", "run", target], env=env_vars)

    log_success("All images built and pushed.")


def step_app(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Deploys xmc_app group (Helm release).

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone.
        dns_zone: Cloud DNS Managed Zone name.
    """
    log_info("Deploying xmc_app group (Helm release)...")
    run_gcluster_deploy(config["deployment_name"], "xmc_app")
    log_success("XMC application Helm release deployed successfully.")


def _wait_for_gateway_programmed() -> None:
    """Waits for the GKE Gateway to be programmed.

    Raises:
        RuntimeError: If the Gateway is not programmed within 300 seconds.
    """
    log_info("Verifying routing Gateway programmed state (5m timeout)...")
    try:
        run_command(
            [
                "kubectl",
                "wait",
                "--for=condition=Programmed",
                "gateway/xmc-gateway",
                "-n",
                "xmc",
                "--timeout=300s",
            ]
        )
    except subprocess.CalledProcessError as e:
        raise RuntimeError(
            "GKE Gateway failed to program within 300s. Run: kubectl describe"
            " gateway xmc-gateway -n xmc"
        ) from e
    log_success("GKE Gateway is Programmed.")


def _wait_for_cert_active(project_id: str, cert_name: str) -> None:
    """Polls Certificate Manager until the certificate is ACTIVE.

    Args:
        project_id: GCP Project ID.
        cert_name: Name of the certificate to check.

    Raises:
        RuntimeError: If the certificate does not become ACTIVE within 15 minutes.
        subprocess.CalledProcessError: If a fatal gcloud command error occurs.
    """
    log_info("Verifying Certificate manager state (15m timeout)...")
    cert_state = ""
    for i in range(1, 91):
        try:
            cert_state = run_command(
                [
                    "gcloud",
                    "certificate-manager",
                    "certificates",
                    "describe",
                    cert_name,
                    f"--project={project_id}",
                    "--format=value(managed.state)",
                ],
                capture_output=True,
                log_on_error=False,
            ).stdout.strip()
        except subprocess.CalledProcessError as e:
            # If it's a "NOT_FOUND" error, we assume it's transient (not created yet)
            # and treat it as PENDING.
            # Otherwise, we propagate the error immediately.
            stderr = e.stderr or ""
            if "NOT_FOUND" in stderr or "not found" in stderr.lower():
                cert_state = "PENDING"
            else:
                log_error(f"Fatal error checking certificate status: {stderr}")
                raise e

        log_info(f"Checking certificate status (attempt {i}/90): {cert_state}")
        if cert_state == "ACTIVE":
            break
        time.sleep(10)

    if cert_state != "ACTIVE":
        raise RuntimeError("Managed Certificate failed to activate within 900s.")

    log_success("Managed Certificate is ACTIVE.")


def step_verify(config: dict[str, Any], dns_project: str, dns_zone: str) -> None:
    """Verifies the deployed XMC environment.

    Args:
        config: The loaded configuration dictionary.
        dns_project: GCP Project ID hosting the DNS zone (unused).
        dns_zone: Cloud DNS Managed Zone name (unused).
    """
    cluster_name = f"xmc-{config['deployment_name']}-{config['environment']}"
    log_info(f"Fetching kubectl credentials for GKE cluster '{cluster_name}'...")
    run_command(
        [
            "gcloud",
            "container",
            "clusters",
            "get-credentials",
            cluster_name,
            f"--region={config['region']}",
            f"--project={config['project_id']}",
        ]
    )

    log_info("Running verification script...")
    env_vars = {
        "ENVIRONMENT": config["environment"],
        "REGION": config["region"],
        "PROJECT_ID": config["project_id"],
    }
    try:
        run_command(
            [sys.executable, "./deploy/ctk_v2/verify_xmc.py"],
            env=env_vars,
            log_on_error=False,
        )
    except subprocess.CalledProcessError:
        print("Diagnostic script reported warnings.")

    _wait_for_gateway_programmed()
    cert_name = f"xmc-cert-{config['environment']}"
    _wait_for_cert_active(config["project_id"], cert_name)
    log_success("XMC environment deployed and verified.")


def main(argv: list[str]) -> None:
    """Main entry point for the deployment script.

    Args:
        argv: Command line arguments.
    """
    ensure_venv()
    args = parse_args(argv)
    config = load_and_validate_config(args.config)

    # Run validations (corresponds to run_checks in bash)
    run_checks(config, args.dns_project, args.dns_zone)

    # Define step mapping
    step_map = {
        "check": lambda: step_check(config, args.dns_project, args.dns_zone),
        "compile": lambda: step_compile(config, args.dns_project, args.dns_zone, args.config),
        "images": lambda: step_images(config, args.dns_project, args.dns_zone),
        "apis": lambda: step_apis(config, args.dns_project, args.dns_zone),
        "primary": lambda: step_primary(config, args.dns_project, args.dns_zone),
        "compute": lambda: step_compute(config, args.dns_project, args.dns_zone),
        "gateway-api": lambda: step_gateway_api(config, args.dns_project, args.dns_zone),
        "prereqs": lambda: step_prereqs(config, args.dns_project, args.dns_zone),
        "app": lambda: step_app(config, args.dns_project, args.dns_zone),
        "verify": lambda: step_verify(config, args.dns_project, args.dns_zone),
    }

    # Determine execution list
    if args.only:
        execution_steps = [args.only]
    elif args.start_from:
        start_index = STEPS.index(args.start_from)
        execution_steps = STEPS[start_index:]
    else:
        execution_steps = STEPS

    current_step = ""
    try:
        for step in execution_steps:
            current_step = step
            step_map[step]()
    except Exception as e:
        log_error(f"Command failed during step '{current_step}': {e}")
        dns_args = f"--dns-project {args.dns_project} --dns-zone {args.dns_zone} "
        # Suggest resume command
        resume_cmd = f"python3 tools/deploy_dev_env.py --config {args.config} {dns_args}"
        if args.only:
            resume_cmd += f"--only {current_step}"
        else:
            resume_cmd += f"--start-from {current_step}"

        print(f"\n{RED}You can resume from this step using:{NC}")
        print(f"  {BOLD}{resume_cmd}{NC}\n")
        sys.exit(1)

    # Print success message if all steps executed succeeded
    print(f"\n{GREEN}" + "=" * 56 + f"{NC}")
    print(f"{GREEN}Deployment Completed Successfully!{NC}")
    print(f"{GREEN}" + "=" * 56 + f"{NC}")
    env = config["environment"]
    dom = config["domain_name"]
    print(f"Final URL: {BOLD}https://{env}.{dom}{NC}")
    print()
    print(f"{BOLD}Post-Deployment Action Required (OAuth Setup):{NC}")
    print("Add this redirect URI in your GCloud Console OAuth Client ID config:")
    print(f"  - {BOLD}https://{env}.{dom}/api/callback{NC}")
    print(f"{GREEN}" + "=" * 56 + f"{NC}")


if __name__ == "__main__":
    main(sys.argv[1:])
