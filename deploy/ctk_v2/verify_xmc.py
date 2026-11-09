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

import json
import os
import pathlib
import re
import subprocess
import sys
import time

# Ensure repo root is in sys.path to allow importing tools
_repo_root = pathlib.Path(__file__).resolve().parent.parent.parent
if str(_repo_root) not in sys.path:
    sys.path.append(str(_repo_root))

from tools.common.tool_utils import ensure_venv  # noqa: E402


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


def _parse_bool(val: str | bool | None) -> bool:
    """Parses boolean from string or boolean representation.

    Args:
      val: Input string, boolean, or None.

    Returns:
      True if string is truthy ('true', '1', 'yes') or if val is True, False otherwise.
    """
    if isinstance(val, bool):
        return val
    if not val:
        return False
    return val.strip().lower() in ("true", "1", "yes")


def _resolve_config(
    config_path: pathlib.Path | None = None,
) -> tuple[str | None, str | None, str | None]:
    """Resolves environment, region, and project_id from environment or YAML config.

    Args:
      config_path: Optional explicit Path to xmanager-cloud-deployment.yaml.

    Returns:
      Tuple of (environment, region, project_id).
    """
    env_val = os.environ.get("ENVIRONMENT")
    region_val = os.environ.get("REGION")
    project_val = os.environ.get("PROJECT_ID")

    if env_val and region_val and project_val:
        return env_val, region_val, project_val

    if config_path is None:
        config_path = pathlib.Path(__file__).parent / "xmanager-cloud-deployment.yaml"

    if not config_path.is_file():
        return env_val, region_val, project_val

    try:
        content = config_path.read_text(encoding="utf-8")
    except OSError:
        return env_val, region_val, project_val

    env_val = env_val or _extract_yaml_val(content, "environment")
    region_val = region_val or _extract_yaml_val(content, "region")
    project_val = project_val or _extract_yaml_val(content, "project_id")
    return env_val, region_val, project_val


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


def _are_pods_healthy(pods_out: str) -> bool:
    """Verifies that pod table output contains healthy pods.

    Args:
      pods_out: Output string from `kubectl get pods -n xmc`.

    Returns:
      True if at least one pod is present and all active pods are Running
      (N/N ready) or Completed/Succeeded/Terminating/Evicted, False otherwise.
    """
    pods_count = 0
    for line in pods_out.strip().splitlines()[1:]:
        cols = line.split()
        if len(cols) < 3 or not re.match(r"^\d+/\d+$", cols[1]):
            continue
        ready_col, status_col = cols[1], cols[2]
        if status_col in ("Completed", "Succeeded", "Terminating", "Evicted"):
            continue
        ready, _, total = ready_col.partition("/")
        if status_col != "Running" or ready != total or ready == "0":
            return False
        pods_count += 1
    return pods_count > 0


def _check_k8s_resources() -> bool:
    """Queries and displays basic GKE Kubernetes resource statuses.

    Returns:
      True if pod query succeeded and all pods are healthy, False otherwise.
    """
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

    pods_out = ""
    for header, cmd in steps:
        print(f"\n--- {header} ---")
        out = _run_command(cmd)
        if header == "1. Pod Status":
            pods_out = out
        if out:
            sys.stdout.write(out)

    return _are_pods_healthy(pods_out)


def _check_connectivity() -> bool:
    """Inspects GKE Gateway IP address and actuator pod port forwarding info.

    Returns:
      True if both Gateway IP and actuator pod name are non-empty, False otherwise.
    """
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
    print(f"Gateway IP Address: {gw_ip}\nMake sure your domains resolve to {gw_ip}.")
    print("\nTo test local endpoint routing internally (Port-Forwarding Actuator):")
    pod_jp = "jsonpath={.items[0].metadata.name}"
    actuator_pod_cmd = ["kubectl", "get", "pods", "-n", "xmc", "-l", "app=actuator", "-o", pod_jp]
    actuator_pod = _run_command(actuator_pod_cmd).strip()
    print(f"Run: kubectl port-forward -n xmc {actuator_pod} 8080:8080")
    print("In another terminal run: curl http://localhost:8080/actuate/health")
    return bool(gw_ip and actuator_pod)


def _resolve_log_monitor_deployment_enabled(
    config_path: pathlib.Path | None = None,
) -> bool:
    """Resolves log_monitor_deployment_enabled from environment or YAML config.

    Args:
      config_path: Optional explicit Path to xmanager-cloud-deployment.yaml.

    Returns:
      True if log_monitor_deployment_enabled is enabled, False otherwise.
    """
    env_flag = os.environ.get("LOG_MONITOR_DEPLOYMENT_ENABLED")
    if env_flag is not None:
        return _parse_bool(env_flag)

    if config_path is None:
        config_path = pathlib.Path(__file__).parent / "xmanager-cloud-deployment.yaml"

    if not config_path.is_file():
        return True

    try:
        content = config_path.read_text(encoding="utf-8")
    except OSError:
        return True

    val = _extract_yaml_val(content, "log_monitor_deployment_enabled")
    if val is None:
        return True
    return _parse_bool(val)


def _check_log_monitor(
    environment: str | None,
    region: str | None,
    project_id: str | None,
    log_monitor_deployment_enabled: bool = False,
) -> bool:
    """Checks readiness status of Cloud Run Log Monitor service when enabled.

    Args:
      environment: Target deployment environment.
      region: Target GCP region.
      project_id: Target GCP project ID.
      log_monitor_deployment_enabled: Whether Log Monitor runs as a GKE Deployment.

    Returns:
      True if Log Monitor check passes (or is handled by GKE deployment), False otherwise.
    """
    print("\n--- 7. Cloud Run Log Monitor Status ---")
    if log_monitor_deployment_enabled:
        print(
            "ℹ️ Skipping Cloud Run Log Monitor check: log_monitor_deployment_enabled is true (checked via GKE Pod Status)."
        )
        return True

    if not environment or not region or not project_id:
        print("⚠️ Skipping Log Monitor check: ENVIRONMENT, REGION, or PROJECT_ID is not set.")
        return False

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
        return False

    print(f"Service Status (Ready): {status}")

    if status != "True":
        print("❌ Log Monitor Cloud Run service is NOT Ready.")
        return False

    print("✅ Log Monitor Cloud Run service is running and Ready.")
    return True


def _parse_k8s_items(json_str: str) -> list[dict]:
    """Parses items list from kubectl JSON output."""
    try:
        data = json.loads(json_str) if json_str else {}
        items = data.get("items") if isinstance(data, dict) else None
        return [i for i in (items or []) if isinstance(i, dict)]
    except json.JSONDecodeError:
        return []


def _is_pod_ready(pod_json_str: str) -> bool:
    """Returns True if at least one pod in the kubectl JSON list is Running and Ready."""
    return any(
        (pod.get("status") or {}).get("phase") == "Running"
        and any(
            isinstance(c, dict) and c.get("type") == "Ready" and c.get("status") == "True"
            for c in ((pod.get("status") or {}).get("conditions") or [])
        )
        for pod in _parse_k8s_items(pod_json_str)
    )


def _are_component_crs_ready(crs_json_str: str) -> tuple[bool, int, str]:
    """Returns (all_ready, count, unready_name) for Component CRs in kubectl JSON."""
    if not crs_json_str.strip():
        return False, 0, ""
    try:
        data = json.loads(crs_json_str)
    except json.JSONDecodeError:
        return False, 0, ""
    if not isinstance(data, dict) or not isinstance(data.get("items"), list):
        return False, 0, ""
    items = [i for i in data["items"] if isinstance(i, dict)]
    for item in items:
        enabled = (item.get("spec") or {}).get("enabled", True)
        status = item.get("status") or {}
        conds = {
            c.get("type"): c.get("status")
            for c in (status.get("conditions") or [])
            if isinstance(c, dict)
        }
        st, sfx = status.get("state"), ("ENABLED", "READY") if enabled else ("DISABLED", "DISABLED")
        if (
            conds.get("Stalled") == "True"
            or conds.get("Synced") == "False"
            or conds.get("Ready") != str(enabled)
            or (st and st not in (f"COMPONENT_STATE_{sfx[0]}", f"STATE_{sfx[1]}"))
        ):
            return False, len(items), str((item.get("metadata") or {}).get("name", "unknown"))
    return True, len(items), ""


def _wait_for_xmc_deployments() -> None:
    """Waits for all Deployments in namespace xmc (including 1P component workloads) to roll out."""
    _run_command(["kubectl", "rollout", "status", "deployment", "-n", "xmc", "--timeout=180s"])


def _check_components(*, max_attempts: int = 18, retry_interval_s: float = 5.0) -> bool:
    """Checks readiness of foundational component controllers and installed Component CRs.

    Args:
      max_attempts: Maximum polling attempts for Component CR reconciliation.
      retry_interval_s: Sleep interval in seconds between polling attempts.

    Returns:
      True if CRD, xmc-bridge pod, and all installed Component CRs are healthy, False otherwise.
    """
    print("\n--- 8. Component Controllers Status ---")
    crd_out = _run_command(["kubectl", "get", "crd", "components.xmc.dev", "-o", "name"]).strip()
    if not crd_out:
        print("❌ Component CRD (components.xmc.dev) not found in cluster.")
        return False
    print(f"✅ Component CRD found: {crd_out}")
    pods_cmd = ["kubectl", "get", "pods", "-n", "xmc", "-l", "app=xmc-bridge", "-o", "json"]
    if not _is_pod_ready(_run_command(pods_cmd)):
        print("❌ xmc-bridge pod is not Running and Ready in namespace xmc.")
        return False
    print("✅ xmc-bridge pod is Running and Ready.")
    crs_cmd = ["kubectl", "get", "components.xmc.dev", "-n", "xmc", "-o", "json"]
    attempts, last_unready = max(1, max_attempts), ""
    for attempt in range(attempts):
        ready, count, last_unready = _are_component_crs_ready(_run_command(crs_cmd))
        if ready:
            if count == 0:
                print(
                    "ℹ️ No Component CRs installed yet (xmc-profile optional); xmc-bridge and CRD are healthy."
                )
            else:
                print(f"✅ All {count} Component CRs verified in expected state.")
            return True
        if attempt + 1 < attempts:
            time.sleep(retry_interval_s)
    if last_unready:
        print(f"❌ Component CR '{last_unready}' not Ready.")
    print("❌ Component CRs verification failed after waiting for reconciliation.")
    return False


def main() -> int:
    """Executes full diagnostic verification suite against target XMC environment.

    Returns:
      Exit code 0 on success, 1 on any check warning/failure.
    """
    ensure_venv()
    print("=========================================")
    print("XManager on Cloud (XMC) v2 Verification")
    print("=========================================")

    env, region, project_id = _resolve_config()
    log_monitor_deployment_enabled = _resolve_log_monitor_deployment_enabled()
    components_ok = _check_components()
    if components_ok:
        _wait_for_xmc_deployments()
    checks = [
        components_ok,
        _check_k8s_resources(),
        _check_connectivity(),
        _check_log_monitor(env, region, project_id, log_monitor_deployment_enabled),
    ]

    print("=========================================")
    return 0 if all(checks) else 1


if __name__ == "__main__":
    sys.exit(main())
