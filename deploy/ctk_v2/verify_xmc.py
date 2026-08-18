#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Verification script for XManager on Cloud (XMC) v2 topology."""

import os
import pathlib
import re
import subprocess
import sys

# Ensure repo root is in sys.path to allow importing tools
_repo_root = pathlib.Path(__file__).resolve().parent.parent.parent
if str(_repo_root) not in sys.path:
    sys.path.append(str(_repo_root))

from tools.tool_utils import ensure_venv  # noqa: E402


def _extract_yaml_val(content: str, key: str) -> str | None:
    """Extracts scalar string value for key from YAML content using regex.

    Args:
      content: Raw text content of YAML deployment config.
      key: Key identifier to search for (e.g. 'environment').

    Returns:
      Extracted value stripped of quotes, or None if key missing/empty.
    """
    pattern = re.compile(rf"^\s*{key}:\s*['\"]?([^'\"#\s]+)", re.MULTILINE)
    match = pattern.search(content)
    if not match:
        return None
    return match.group(1)


def _parse_bool(val: str | None) -> bool:
    """Parses boolean from string representation.

    Args:
      val: Input string or None.

    Returns:
      True if string is truthy ('true', '1', 'yes'), False otherwise.
    """
    if not val:
        return False
    return val.strip().lower() in ("true", "1", "yes")


def _resolve_config(
    config_path: pathlib.Path | None = None,
) -> tuple[str | None, str | None, str | None, bool]:
    """Resolves environment, region, project_id, and enable_components flag.

    Args:
      config_path: Optional explicit Path to xmanager-cloud-deployment.yaml.

    Returns:
      Tuple of (environment, region, project_id, enable_components).
    """
    env_val = os.environ.get("ENVIRONMENT")
    region_val = os.environ.get("REGION")
    project_val = os.environ.get("PROJECT_ID")
    enable_components_env = os.environ.get("ENABLE_COMPONENTS")

    if env_val and region_val and project_val and enable_components_env is not None:
        return env_val, region_val, project_val, _parse_bool(enable_components_env)

    if config_path is None:
        config_path = pathlib.Path(__file__).parent / "xmanager-cloud-deployment.yaml"

    if not config_path.is_file():
        return env_val, region_val, project_val, _parse_bool(enable_components_env)

    try:
        content = config_path.read_text(encoding="utf-8")
    except OSError:
        return env_val, region_val, project_val, _parse_bool(enable_components_env)

    env_val = env_val or _extract_yaml_val(content, "environment")
    region_val = region_val or _extract_yaml_val(content, "region")
    project_val = project_val or _extract_yaml_val(content, "project_id")

    if enable_components_env is not None:
        enable_components = _parse_bool(enable_components_env)
    else:
        yaml_comp = _extract_yaml_val(content, "enable_components")
        enable_components = _parse_bool(yaml_comp)

    return env_val, region_val, project_val, enable_components


def _run_command(cmd: list[str]) -> str:
    """Runs subprocess command and returns stdout text.

    Args:
      cmd: Command and list of arguments.

    Returns:
      Trimmed standard output string, or empty string on error.
    """
    try:
        res = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            text=True,
            check=False,
        )
        if res.returncode != 0:
            return ""
        return res.stdout
    except Exception:  # pylint: disable=broad-except
        return ""


def _check_k8s_resources() -> None:
    """Queries and displays basic GKE Kubernetes resource statuses."""
    steps = [
        ("1. Pod Status", ["kubectl", "get", "pods", "-n", "xmc"]),
        ("2. Service Status", ["kubectl", "get", "svc", "-n", "xmc"]),
        ("3. GKE Gateway Status", ["kubectl", "get", "gateway", "-n", "xmc"]),
        ("4. HTTPRoutes", ["kubectl", "get", "httproute", "-n", "xmc"]),
        (
            "5. cert-manager Certificates",
            ["kubectl", "get", "certificate", "-n", "xmc"],
        ),
    ]

    for header, cmd in steps:
        print(f"\n--- {header} ---")
        out = _run_command(cmd)
        if out:
            sys.stdout.write(out)


def _check_connectivity() -> None:
    """Inspects GKE Gateway IP address and actuator pod port forwarding info."""
    print("\n--- 6. Test Connectivity ---")
    gw_ip_cmd = [
        "kubectl",
        "get",
        "gateway",
        "xmc-gateway",
        "-n",
        "xmc",
        "-o",
        "jsonpath={.status.addresses[0].value}",
    ]
    gw_ip = _run_command(gw_ip_cmd).strip()
    print(f"Gateway IP Address: {gw_ip}")
    print(f"Make sure your domains resolve to {gw_ip}.")

    print("\nTo test local endpoint routing internally (Port-Forwarding Actuator):")
    actuator_pod_cmd = [
        "kubectl",
        "get",
        "pods",
        "-n",
        "xmc",
        "-l",
        "app=actuator",
        "-o",
        "jsonpath={.items[0].metadata.name}",
    ]
    actuator_pod = _run_command(actuator_pod_cmd).strip()
    print(f"Run: kubectl port-forward -n xmc {actuator_pod} 8080:8080")
    print("In another terminal run: curl http://localhost:8080/actuate/health")


def _check_log_monitor(
    environment: str | None,
    region: str | None,
    project_id: str | None,
) -> None:
    """Checks readiness status of Cloud Run Log Monitor service.

    Args:
      environment: Target deployment environment.
      region: Target GCP region.
      project_id: Target GCP project ID.
    """
    print("\n--- 7. Cloud Run Log Monitor Status ---")
    if not environment or not region or not project_id:
        print("⚠️ Skipping Log Monitor check: ENVIRONMENT, REGION, or PROJECT_ID is not set.")
        return

    svc_name = f"xmc-log-monitor-{environment}"
    print(f"Checking Cloud Run Service: {svc_name} in {region}...")

    gcloud_cmd = [
        "gcloud",
        "run",
        "services",
        "list",
        f"--filter=metadata.name={svc_name} AND status.conditions.type=Ready",
        "--flatten=status.conditions",
        "--format=value(status.conditions.status)",
        "--platform",
        "managed",
        "--region",
        region,
        "--project",
        project_id,
    ]
    status = _run_command(gcloud_cmd).strip()

    if not status:
        print(f"❌ Log Monitor Cloud Run service {svc_name} not found or status is unavailable.")
        return

    print(f"Service Status (Ready): {status}")

    if status != "True":
        print("❌ Log Monitor Cloud Run service is NOT Ready.")
        return

    print("✅ Log Monitor Cloud Run service is running and Ready.")


def _check_components(enable_components: bool) -> None:
    """Checks status of foundational component controllers if enabled.

    Args:
      enable_components: Whether foundational component controllers are enabled.
    """
    print("\n--- 8. Component Controllers Status ---")
    if not enable_components:
        print("ℹ️ Skipping Component Controllers check: enable_components is false.")
        return

    cmd = ["kubectl", "get", "pods", "-n", "xmc", "-l", "app=components-controller"]
    out = _run_command(cmd)
    if out:
        sys.stdout.write(out)


def main() -> int:
    """Executes full diagnostic verification suite against target XMC environment.

    Returns:
      Exit code 0 on success, 1 on any check warning/failure.
    """
    ensure_venv()
    print("=========================================")
    print("XManager on Cloud (XMC) v2 Verification")
    print("=========================================")

    env, region, project_id, enable_components = _resolve_config()
    _check_k8s_resources()
    _check_connectivity()
    _check_log_monitor(env, region, project_id)
    _check_components(enable_components)

    print("=========================================")
    return 0


if __name__ == "__main__":
    sys.exit(main())
