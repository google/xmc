# ruff: noqa: E402
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

"""Cleans up orphaned GKE Gateway global forwarding rules (gkegw1-*).

This script identifies and deletes stale global forwarding rules created by the GKE
Gateway controller. When a cluster is destroyed abruptly (e.g., via Terraform/gcluster destroy
without first deleting the Gateway resource), these rules are left behind and block
cluster redeployment from binding to static IPs.
"""

import argparse
import json
import pathlib
import subprocess
import sys

_REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import GREEN, NC, RED, load_and_validate_config


def run_cmd(cmd: list[str]) -> str:
    """Runs a shell command and returns its stdout."""
    res = subprocess.run(cmd, stdout=subprocess.PIPE, text=True, check=False)
    if res.returncode != 0:
        raise RuntimeError(f"Command failed with exit code {res.returncode}: {' '.join(cmd)}")
    return res.stdout.strip()


def get_static_ip(project_id: str, environment: str) -> str | None:
    """Retrieves the static IP address for the given environment."""
    ip_name = f"xmc-gateway-static-ip-{environment}"
    cmd = [
        "gcloud",
        "compute",
        "addresses",
        "describe",
        ip_name,
        "--global",
        f"--project={project_id}",
        "--format=value(address)",
    ]
    try:
        return run_cmd(cmd)
    except RuntimeError:
        return None


def find_forwarding_rules(project_id: str, ip_address: str) -> list[str]:
    """Finds global forwarding rules binding to the specified IP address."""
    cmd = [
        "gcloud",
        "compute",
        "forwarding-rules",
        "list",
        "--global",
        f"--project={project_id}",
        f"--filter=IPAddress={ip_address}",
        "--format=json",
    ]
    try:
        output = run_cmd(cmd)
        if not output:
            return []
        rules = json.loads(output)
        return [r["name"] for r in rules if r.get("name", "").startswith("gkegw1-")]
    except RuntimeError:
        return []


def delete_forwarding_rule(project_id: str, rule_name: str, dry_run: bool = False) -> None:
    """Deletes a specific global forwarding rule."""
    if dry_run:
        print(f"[{GREEN}Dry-Run{NC}] Would delete global forwarding rule: {rule_name}")
        return
    print(f"Deleting global forwarding rule: {rule_name}...")
    cmd = [
        "gcloud",
        "compute",
        "forwarding-rules",
        "delete",
        rule_name,
        "--global",
        f"--project={project_id}",
        "--quiet",
    ]
    try:
        run_cmd(cmd)
        print(f"{GREEN}Successfully deleted forwarding rule: {rule_name}{NC}")
    except RuntimeError as e:
        print(f"{RED}Failed to delete {rule_name}: {e}{NC}")


def main(argv: list[str] | None = None) -> None:
    if argv is None:
        argv = sys.argv[1:]

    parser = argparse.ArgumentParser(description="Cleanup orphaned GKE Gateway forwarding rules.")
    parser.add_argument("-c", "--config", required=True, help="Path to deployment YAML config.")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print rules to be deleted without actually deleting them.",
    )

    args = parser.parse_args(argv)

    config = load_and_validate_config(args.config)

    print(
        f"Looking for static IP 'xmc-gateway-static-ip-{config.environment}' in project '{config.project_id}'..."
    )
    ip_address = get_static_ip(config.project_id, config.environment)
    if not ip_address:
        print(f"{RED}No static IP found for environment '{config.environment}'. Exiting.{NC}")
        sys.exit(0)

    print(f"{GREEN}Found static IP for '{config.environment}': {ip_address}{NC}")

    print(f"Searching for orphaned forwarding rules binding to {ip_address}...")
    rules = find_forwarding_rules(config.project_id, ip_address)
    if not rules:
        print(f"{GREEN}No orphaned 'gkegw1-*' forwarding rules found binding to {ip_address}.{NC}")
        sys.exit(0)

    print(f"{RED}Found {len(rules)} orphaned forwarding rule(s) blocking the IP.{NC}")
    for rule in rules:
        delete_forwarding_rule(config.project_id, rule, args.dry_run)


if __name__ == "__main__":
    main()
