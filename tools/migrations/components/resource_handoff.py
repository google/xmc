# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Transfers Helm ownership annotations on legacy Ingestion Gateway resources."""

import subprocess
import sys
from typing import Any

from tools.common.tool_utils import HandledError, run_command
from tools.migrations.components.cluster_inspector import (
    GATEWAY_COMPONENT_RELEASE,
    GATEWAY_DEPLOYMENT_NAME,
    GATEWAY_HPA_NAME,
    GATEWAY_SERVICE_NAME,
    annotate_resource,
    fetch_live_resource,
    helm_release_exists,
    validate_dns1123_namespace,
    validate_helm_release,
)


def _verify_owner_metadata(
    resource: dict[str, Any], kind: str, name: str, owners: set[str], ns: str
) -> None:
    """Verifies Helm managed-by label and release ownership annotations match expected owners."""
    meta = resource["metadata"] if isinstance(resource.get("metadata"), dict) else {}
    annotations = meta["annotations"] if isinstance(meta.get("annotations"), dict) else {}
    labels = meta["labels"] if isinstance(meta.get("labels"), dict) else {}
    owner = annotations.get("meta.helm.sh/release-name", "")
    owner_ns = annotations.get("meta.helm.sh/release-namespace", "")
    if labels.get("app.kubernetes.io/managed-by") != "Helm":
        raise HandledError(
            f"{kind}/{name} is missing label 'app.kubernetes.io/managed-by=Helm'.",
            f"Run 'kubectl label {kind} {name} -n {ns} app.kubernetes.io/managed-by=Helm --overwrite'.",
        )
    if owner not in owners or owner_ns != ns:
        raise HandledError(
            f"{kind}/{name} has unexpected Helm owner ({owner!r}, namespace={owner_ns!r}).",
            f"Expected release in {sorted(owners)} and namespace '{ns}'.",
        )


def _uninstall_orphaned_component_release(ns: str, targets: list[tuple[str, str]]) -> None:
    """Safely uninstalls orphaned xmc-cmp-ingestion-gateway release after stamping keep."""
    try:
        cr = fetch_live_resource("components.xmc.dev", "ingestion-gateway", ns, required=False)
    except HandledError as e:
        if "resource type" not in str(e).lower() and "no matches for kind" not in str(e).lower():
            raise
        cr = None
    if cr is not None:
        raise HandledError(
            f"Cannot undo handoff while Component/ingestion-gateway exists in '{ns}'.",
            "Uninstall xmc-profile with deletionPolicy=Orphan first before running handoff --undo.",
        )
    if not helm_release_exists(GATEWAY_COMPONENT_RELEASE, ns):
        return
    keep = ["helm.sh/resource-policy=keep"]
    for kind, name in targets:
        annotate_resource(kind, name, ns, keep)
    if fetch_live_resource("networkpolicy", "ingestion-gateway-network-policy", ns, required=False):
        annotate_resource("networkpolicy", "ingestion-gateway-network-policy", ns, keep)
    try:
        cmd = ["helm", "uninstall", "-n", ns, "--", GATEWAY_COMPONENT_RELEASE]
        run_command(cmd, capture_output=True, log_on_error=False, stream=sys.stderr)
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        err = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(f"Failed to uninstall '{GATEWAY_COMPONENT_RELEASE}': {err}", "Retry.")


def execute_resource_handoff(release: str, namespace: str, *, undo: bool = False) -> dict[str, Any]:
    """Marks legacy gateway objects 'keep' and re-points Helm ownership (or reverts with undo)."""
    validate_dns1123_namespace(namespace)
    validate_helm_release(release)
    targets = _collect_validated_targets(release, namespace)
    if undo:
        _uninstall_orphaned_component_release(namespace, targets)
    netpol_name = f"{release}-ingestion-gateway-network-policy"
    netpol = fetch_live_resource("networkpolicy", netpol_name, namespace, required=False)
    if netpol is not None:
        _verify_owner_metadata(netpol, "networkpolicy", netpol_name, {release}, namespace)
    target_owner = release if undo else GATEWAY_COMPONENT_RELEASE
    policy_arg = "helm.sh/resource-policy-" if undo else "helm.sh/resource-policy=keep"
    owner_args = [policy_arg, f"meta.helm.sh/release-name={target_owner}"]
    owner_args.append(f"meta.helm.sh/release-namespace={namespace}")
    updated: list[str] = []
    for kind, name in targets:
        annotate_resource(kind, name, namespace, owner_args)
        updated.append(f"{kind}/{name}")
    if netpol is not None:
        annotate_resource("networkpolicy", netpol_name, namespace, [policy_arg])
        updated.append(f"networkpolicy/{netpol_name}")
    return {
        "action": "handoff_undo" if undo else "handoff",
        "namespace": namespace,
        "target_owner": target_owner,
        "updated_resources": updated,
    }


def _collect_validated_targets(release: str, ns: str) -> list[tuple[str, str]]:
    """Fetches Deployment, Service, and optional HPA and verifies their current Helm owners."""
    targets = [("deployment", GATEWAY_DEPLOYMENT_NAME), ("service", GATEWAY_SERVICE_NAME)]
    hpa = fetch_live_resource("horizontalpodautoscaler", GATEWAY_HPA_NAME, ns, required=False)
    if hpa is not None:
        targets.append(("horizontalpodautoscaler", GATEWAY_HPA_NAME))
    allowed_owners = {release, GATEWAY_COMPONENT_RELEASE}
    for kind, name in targets:
        obj = hpa if kind == "horizontalpodautoscaler" else fetch_live_resource(kind, name, ns)
        assert isinstance(obj, dict)
        _verify_owner_metadata(obj, kind, name, allowed_owners, ns)
    return targets
