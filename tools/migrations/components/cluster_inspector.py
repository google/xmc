# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Read-only Kubernetes and Helm inspection queries for component migration."""

import json
import subprocess
import sys
from typing import Any

from tools.common.tool_utils import HandledError, run_command

GATEWAY_DEPLOYMENT_NAME = "ingestion-gateway-deployment"
GATEWAY_HPA_NAME = "ingestion-gateway-hpa"


def fetch_umbrella_values(release: str, namespace: str) -> dict[str, Any]:
    """Fetches computed Helm values (defaults + overrides) for the umbrella release."""
    try:
        res = run_command(
            ["helm", "get", "values", release, "-n", namespace, "-a", "-o", "json"],
            capture_output=True,
            log_on_error=False,
            stream=sys.stderr,
        )
        parsed = json.loads(res.stdout or "{}")
    except (FileNotFoundError, subprocess.CalledProcessError, json.JSONDecodeError) as e:
        stderr = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(
            f"Failed to read Helm values for release '{release}' in '{namespace}': {stderr}",
            f"Verify helm is installed and 'helm list -n {namespace}' shows '{release}'.",
        ) from e
    return parsed if isinstance(parsed, dict) else {}


def fetch_live_resource(
    kind: str, name: str, namespace: str, *, required: bool = True
) -> dict[str, Any] | None:
    """Fetches a live Kubernetes object as a dict, returning None when optional and NotFound."""
    try:
        res = run_command(
            ["kubectl", "get", kind, name, "-n", namespace, "-o", "json"],
            check=False,
            capture_output=True,
            log_on_error=False,
            stream=sys.stderr,
        )
    except FileNotFoundError as e:
        raise HandledError(
            f"kubectl binary not found while fetching {kind}/{name}: {e}",
            "Install kubectl on PATH before running migrate.py.",
        ) from e
    if res.returncode != 0:
        err_text = (res.stderr or res.stdout or "").strip()
        if not required and ("notfound" in err_text.lower() or "not found" in err_text.lower()):
            return None
        raise HandledError(
            f"Failed to fetch {kind}/{name} in namespace '{namespace}': {err_text}",
            f"Verify {kind}/{name} exists in namespace '{namespace}' and kubectl has access.",
        )
    try:
        data = json.loads(res.stdout)
    except json.JSONDecodeError:
        data = None
    if not isinstance(data, dict):
        raise HandledError(
            f"Invalid JSON returned for {kind}/{name} in namespace '{namespace}'.",
            "Re-run kubectl get manually to inspect cluster API output.",
        )
    return data
