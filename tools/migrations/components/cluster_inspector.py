# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Kubernetes and Helm inspection and annotation primitives for component migration."""

import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_command

GATEWAY_COMPONENT_RELEASE = "xmc-cmp-ingestion-gateway"
GATEWAY_DEPLOYMENT_NAME = "ingestion-gateway-deployment"
GATEWAY_SERVICE_NAME = "ingestion-gateway-service"
GATEWAY_HPA_NAME = "ingestion-gateway-hpa"
_DNS1123_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")


def validate_dns1123_namespace(namespace: str) -> None:
    """Validates that namespace is a non-empty Kubernetes DNS-1123 label."""
    if not namespace or len(namespace) > 63 or not _DNS1123_RE.match(namespace):
        raise HandledError(f"Invalid Kubernetes namespace {namespace!r}.", "Use a DNS-1123 label.")


def annotate_resource(kind: str, name: str, namespace: str, annotations: list[str]) -> None:
    """Applies or removes annotations on a Kubernetes resource via kubectl annotate --overwrite."""
    try:
        run_command(
            ["kubectl", "annotate", kind, name, "-n", namespace, "--overwrite", *annotations],
            capture_output=True,
            log_on_error=False,
            stream=sys.stderr,
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        err = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(
            f"Failed to annotate {kind}/{name} in namespace '{namespace}': {err}",
            "Verify kubectl access and retry.",
        ) from e


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


def verify_secret_exists(secret_name: str, namespace: str, *, field_label: str) -> None:
    """Raises HandledError if the named Kubernetes Secret is absent in namespace."""
    if fetch_live_resource("secret", secret_name, namespace, required=False) is None:
        raise HandledError(
            f"{field_label} Secret '{secret_name}' was not found in namespace '{namespace}'.",
            f"Create Secret '{secret_name}' in namespace '{namespace}' before migrating.",
        )


def helm_release_exists(release: str, namespace: str) -> bool:
    """Returns True if Helm release exists in namespace, False if not found, else raises."""
    try:
        res = run_command(
            ["helm", "status", release, "-n", namespace, "-o", "json"],
            check=False,
            capture_output=True,
            log_on_error=False,
            stream=sys.stderr,
        )
    except FileNotFoundError as e:
        raise HandledError(f"helm binary not found: {e}", "Install helm >= 3.14.0.") from e
    if res.returncode == 0:
        return True
    err_text = (res.stderr or res.stdout or "").strip()
    if "not found" in err_text.lower():
        return False
    raise HandledError(
        f"Failed to check Helm status for '{release}' in '{namespace}': {err_text}",
        "Verify kubectl/helm cluster connectivity and RBAC permissions.",
    )


def fetch_helm_release_status(release: str, namespace: str) -> str:
    """Returns info.status (e.g. 'deployed') for a Helm release in namespace."""
    cmd = ["helm", "status", release, "-n", namespace, "-o", "json"]
    try:
        res = run_command(cmd, capture_output=True, log_on_error=False, stream=sys.stderr)
        data = json.loads(res.stdout or "{}")
    except (FileNotFoundError, subprocess.CalledProcessError, json.JSONDecodeError) as e:
        err = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(f"Helm status failed for '{release}': {err}", "Check helm.") from e
    return str(((data.get("info") or {}) if isinstance(data, dict) else {}).get("status") or "")


def _decode_json_stream(raw: str) -> list[dict[str, Any]]:
    """Parses one or more concatenated JSON objects emitted by kubectl --dry-run=client -o json."""
    decoder, idx, length = json.JSONDecoder(), 0, len(raw)
    items: list[dict[str, Any]] = []
    while idx < length:
        while idx < length and raw[idx].isspace():
            idx += 1
        if idx >= length:
            break
        obj, idx = decoder.raw_decode(raw, idx)
        if isinstance(obj, dict):
            sub = obj.get("items") if obj.get("kind") == "List" else None
            if isinstance(sub, list):
                items.extend(it for it in sub if isinstance(it, dict))
            else:
                items.append(obj)
    return items


def fetch_rendered_release_objects(
    release: str, namespace: str
) -> dict[tuple[str, str], dict[str, Any]]:
    """Returns rendered objects for a Helm release keyed by (kind.lower(), metadata.name)."""
    h_cmd = ["helm", "get", "manifest", release, "-n", namespace]
    try:
        manifest = run_command(
            h_cmd, capture_output=True, log_on_error=False, stream=sys.stderr
        ).stdout
        with tempfile.TemporaryDirectory(prefix="xmc_manifest_") as tmp_dir:
            p = Path(tmp_dir) / "manifest.yaml"
            p.write_text(manifest, encoding="utf-8")
            k_cmd = ["kubectl", "create", "--dry-run=client", "-o", "json", "-f", str(p)]
            raw = run_command(
                k_cmd, capture_output=True, log_on_error=False, stream=sys.stderr
            ).stdout
        objects = _decode_json_stream(raw)
    except (FileNotFoundError, subprocess.CalledProcessError, json.JSONDecodeError) as e:
        err = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(f"Manifest parse failed for '{release}': {err}", "Check helm.") from e
    return {
        (str(o["kind"]).lower(), str(o["metadata"]["name"])): o
        for o in objects
        if o.get("kind") and isinstance(o.get("metadata"), dict) and o["metadata"].get("name")
    }
