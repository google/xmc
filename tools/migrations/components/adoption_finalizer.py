# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Verifies component adoption, checks live drift, strips keep, and probes ingestion."""

import json
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import UTC, datetime
from typing import Any

from tools.common.tool_utils import HandledError, run_command
from tools.migrations.components.cluster_inspector import (
    GATEWAY_COMPONENT_RELEASE,
    GATEWAY_DEPLOYMENT_NAME,
    GATEWAY_HPA_NAME,
    GATEWAY_SERVICE_NAME,
    annotate_resource,
    fetch_helm_release_status,
    fetch_live_resource,
    fetch_rendered_release_objects,
    validate_dns1123_namespace,
)

_COMPONENT_NETPOL_NAME = "ingestion-gateway-network-policy"
_FATAL_WAIT_REASONS = frozenset(
    [
        "CrashLoopBackOff",
        "CreateContainerConfigError",
        "ErrImagePull",
        "ImagePullBackOff",
        "InvalidImageName",
    ]
)


def _wait_for_component_ready(
    namespace: str, timeout_sec: int, poll_interval_sec: float = 2.0
) -> None:
    """Polls Component/ingestion-gateway until Ready=True and Synced=True at metadata.generation."""
    deadline = time.monotonic() + max(timeout_sec, 1)
    last_state = "Component/ingestion-gateway not yet found"
    while True:
        cr = fetch_live_resource(
            "components.xmc.dev", "ingestion-gateway", namespace, required=False
        )
        if cr is not None:
            gen = int((cr.get("metadata") or {}).get("generation") or 1)
            conds = {
                c.get("type"): c
                for c in ((cr.get("status") or {}).get("conditions") or [])
                if isinstance(c, dict)
            }
            ready, synced = conds.get("Ready") or {}, conds.get("Synced") or {}
            if (
                ready.get("status") == "True"
                and synced.get("status") == "True"
                and int(ready.get("observedGeneration") or 0) >= gen
                and int(synced.get("observedGeneration") or 0) >= gen
            ):
                return
            last_state = f"Ready={ready.get('status')}/{ready.get('reason')}, Synced={synced.get('status')}/{synced.get('reason')}"
        if time.monotonic() >= deadline:
            raise HandledError(
                f"Timed out waiting for Component/ingestion-gateway Ready in '{namespace}' ({last_state}).",
                f"Inspect 'kubectl describe component ingestion-gateway -n {namespace}'.",
            )
        time.sleep(poll_interval_sec)


def _assert_component_ownership(obj: dict[str, Any], kind: str, name: str, namespace: str) -> None:
    """Asserts live object carries Helm managed-by label and xmc-cmp-ingestion-gateway ownership."""
    meta = obj.get("metadata") or {}
    ann = meta.get("annotations") or {}
    if (meta.get("labels") or {}).get("app.kubernetes.io/managed-by") != "Helm":
        raise HandledError(f"{kind}/{name} lacks app.kubernetes.io/managed-by=Helm.", "Check CR.")
    if (
        ann.get("meta.helm.sh/release-name") != GATEWAY_COMPONENT_RELEASE
        or ann.get("meta.helm.sh/release-namespace") != namespace
    ):
        raise HandledError(
            f"{kind}/{name} is not owned by '{GATEWAY_COMPONENT_RELEASE}' in '{namespace}'.",
            "Re-run 'migrate.py handoff' and 'helm upgrade --install xmc-profile'.",
        )


def _compare_deployment_and_service(
    live_dep: dict[str, Any],
    ren_dep: dict[str, Any],
    live_svc: dict[str, Any],
    ren_svc: dict[str, Any],
) -> None:
    """Checks live Deployment and Service specs against rendered Helm manifests for drift."""
    gen = int((live_dep.get("metadata") or {}).get("generation") or 1)
    obs = int((live_dep.get("status") or {}).get("observedGeneration") or 0)
    if obs < gen:
        raise HandledError(
            f"Deployment observedGeneration ({obs}) lags generation ({gen}).", "Wait for rollout."
        )
    l_spec = ((live_dep.get("spec") or {}).get("template") or {}).get("spec") or {}
    r_spec = ((ren_dep.get("spec") or {}).get("template") or {}).get("spec") or {}
    l_ctrs, r_ctrs = l_spec.get("containers") or [], r_spec.get("containers") or []
    if [c.get("name") for c in l_ctrs] != [c.get("name") for c in r_ctrs] or not l_ctrs:
        raise HandledError("Adopted Deployment containers drift from rendered chart.", "Inspect.")
    if l_ctrs[0].get("image") != r_ctrs[0].get("image"):
        raise HandledError("Adopted Deployment image drifts from rendered chart.", "Inspect.")
    l_args, r_args = list(l_ctrs[0].get("args") or []), list(r_ctrs[0].get("args") or [])
    if set(l_args) != set(r_args) or "-enable_components=true" not in l_args:
        raise HandledError("Adopted Deployment CLI args drift or lack -enable_components=true.", "")
    l_vols = {v.get("name") for v in (l_spec.get("volumes") or [])}
    r_vols = {v.get("name") for v in (r_spec.get("volumes") or [])}
    if l_vols != r_vols:
        raise HandledError(f"Adopted Deployment volumes drift ({l_vols} != {r_vols}).", "Inspect.")
    l_ports = [
        (p.get("name"), p.get("port"), p.get("targetPort"))
        for p in ((live_svc.get("spec") or {}).get("ports") or [])
    ]
    r_ports = [
        (p.get("name"), p.get("port"), p.get("targetPort"))
        for p in ((ren_svc.get("spec") or {}).get("ports") or [])
    ]
    if l_ports != r_ports:
        raise HandledError("Adopted Service ports drift from rendered chart.", "Inspect Service.")


def _verify_adopted_objects_and_drift(namespace: str) -> tuple[bool, int]:
    """Verifies Helm release status, object ownership, and zero spec drift; returns (has_hpa, min_pods)."""
    status = fetch_helm_release_status(GATEWAY_COMPONENT_RELEASE, namespace)
    if status != "deployed":
        raise HandledError(
            f"Helm release '{GATEWAY_COMPONENT_RELEASE}' status is '{status}', expected 'deployed'.",
            f"Inspect 'helm status {GATEWAY_COMPONENT_RELEASE} -n {namespace}'.",
        )
    rendered = fetch_rendered_release_objects(GATEWAY_COMPONENT_RELEASE, namespace)
    ren_dep = rendered.get(("deployment", GATEWAY_DEPLOYMENT_NAME))
    ren_svc = rendered.get(("service", GATEWAY_SERVICE_NAME))
    if ren_dep is None or ren_svc is None:
        raise HandledError("Rendered component manifest lacks Deployment or Service.", "Check CR.")
    live_dep = fetch_live_resource("deployment", GATEWAY_DEPLOYMENT_NAME, namespace)
    live_svc = fetch_live_resource("service", GATEWAY_SERVICE_NAME, namespace)
    assert isinstance(live_dep, dict) and isinstance(live_svc, dict)
    _assert_component_ownership(live_dep, "deployment", GATEWAY_DEPLOYMENT_NAME, namespace)
    _assert_component_ownership(live_svc, "service", GATEWAY_SERVICE_NAME, namespace)
    _compare_deployment_and_service(live_dep, ren_dep, live_svc, ren_svc)

    ren_hpa = rendered.get(("horizontalpodautoscaler", GATEWAY_HPA_NAME))
    if ren_hpa is None:
        return False, int((ren_dep.get("spec") or {}).get("replicas", 1))
    live_hpa = fetch_live_resource("horizontalpodautoscaler", GATEWAY_HPA_NAME, namespace)
    assert isinstance(live_hpa, dict)
    _assert_component_ownership(live_hpa, "horizontalpodautoscaler", GATEWAY_HPA_NAME, namespace)
    ls, rs = live_hpa.get("spec") or {}, ren_hpa.get("spec") or {}
    if ls.get("minReplicas") != rs.get("minReplicas") or ls.get("maxReplicas") != rs.get(
        "maxReplicas"
    ):
        raise HandledError("Adopted HPA replica bounds drift from rendered chart.", "Inspect HPA.")
    return True, int(rs.get("minReplicas", 1))


def _verify_rollout_and_pods(ns: str, timeout_sec: int, expected_replicas: int) -> int:
    """Waits on Deployment rollout and verifies active Running/Ready pods; returns active count."""
    rollout_cmd = [
        "kubectl",
        "rollout",
        "status",
        f"deployment/{GATEWAY_DEPLOYMENT_NAME}",
        "-n",
        ns,
    ]
    pods_cmd = ["kubectl", "get", "pods", "-n", ns, "-l", "app=ingestion-gateway", "-o", "json"]
    try:
        run_command(
            [*rollout_cmd, f"--timeout={max(timeout_sec, 1)}s"],
            capture_output=True,
            log_on_error=False,
            stream=sys.stderr,
        )
        pods_raw = run_command(
            pods_cmd, capture_output=True, log_on_error=False, stream=sys.stderr
        ).stdout
        items = (json.loads(pods_raw or "{}").get("items")) or []
    except (FileNotFoundError, subprocess.CalledProcessError, json.JSONDecodeError) as e:
        err = (getattr(e, "stderr", "") or str(e)).strip()
        raise HandledError(f"Gateway rollout verification failed: {err}", "Check pods.") from e
    active = [
        p
        for p in items
        if (p.get("status") or {}).get("phase") != "Succeeded"
        and (p.get("status") or {}).get("reason") != "Evicted"
    ]
    if expected_replicas > 0 and not active:
        raise HandledError("No active ingestion-gateway pods found after rollout.", "Check HPA.")
    for pod in active:
        _assert_pod_healthy(pod)
    return len(active)


def _assert_pod_healthy(pod: dict[str, Any]) -> None:
    """Raises HandledError if an active pod is not Running with all containers Ready."""
    name = (pod.get("metadata") or {}).get("name", "<unknown>")
    st = pod.get("status") or {}
    statuses = st.get("containerStatuses") or []
    for cs in statuses:
        reason = (((cs.get("state") or {}).get("waiting")) or {}).get("reason", "")
        if reason in _FATAL_WAIT_REASONS:
            raise HandledError(f"Pod {name} is in fatal waiting state '{reason}'.", "Check logs.")
    if st.get("phase") != "Running" or not statuses or not all(c.get("ready") for c in statuses):
        raise HandledError(f"Pod {name} is not Running/Ready (phase={st.get('phase')!r}).", "Wait.")


def _strip_keep_and_cleanup_netpol(
    release: str, ns: str, *, has_hpa: bool
) -> tuple[list[str], bool]:
    """Removes helm.sh/resource-policy=keep from adopted objects and deletes legacy NetPol."""
    targets = [("deployment", GATEWAY_DEPLOYMENT_NAME), ("service", GATEWAY_SERVICE_NAME)]
    if has_hpa:
        targets.append(("horizontalpodautoscaler", GATEWAY_HPA_NAME))
    stripped: list[str] = []
    for kind, name in targets:
        annotate_resource(kind, name, ns, ["helm.sh/resource-policy-"])
        stripped.append(f"{kind}/{name}")
    legacy_np = f"{release}-ingestion-gateway-network-policy"
    cleaned_np = False
    if fetch_live_resource("networkpolicy", _COMPONENT_NETPOL_NAME, ns, required=False):
        del_cmd = ["kubectl", "delete", "networkpolicy", legacy_np, "-n", ns]
        try:
            run_command(
                [*del_cmd, "--ignore-not-found=true"],
                capture_output=True,
                log_on_error=False,
                stream=sys.stderr,
            )
            cleaned_np = True
        except (FileNotFoundError, subprocess.CalledProcessError) as e:
            err = (getattr(e, "stderr", "") or str(e)).strip()
            raise HandledError(f"Failed deleting {legacy_np}: {err}", "") from e
    return stripped, cleaned_np


def _probe_metrics_endpoint(metrics_url: str, test_xid: int) -> int:
    """Sends an HTTP POST probe to /api/v1/metrics and returns the HTTP status code."""
    parsed = urllib.parse.urlsplit(metrics_url)
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        raise HandledError(
            f"Invalid --metrics-url {metrics_url!r}: scheme must be http or https.",
            "Pass e.g. http://127.0.0.1:8080/api/v1/metrics.",
        )
    ts = int(datetime.now(UTC).timestamp() * 1000)
    sample: dict[str, Any] = {"experiment_id": test_xid, "work_unit_id": 1, "step": 0}
    sample.update({"timestamp": ts, "metric_name": "xmc_migration_probe", "metric_value": 1.0})
    payload: list[dict[str, Any]] = [] if test_xid <= 0 else [sample]
    req = urllib.request.Request(  # noqa: S310
        metrics_url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:  # noqa: S310
            code = int(resp.status)
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        raise HandledError(f"Metrics probe to '{metrics_url}' failed: {e}", "Check gateway.") from e
    if code not in (200, 202):
        raise HandledError(f"Metrics probe returned HTTP {code}, expected 202.", "Check gateway.")
    return code


def finalize_adoption(
    release: str,
    ns: str,
    *,
    timeout_sec: int = 300,
    metrics_url: str = "",
    test_xid: int = 0,
    skip_probe: bool = False,
) -> dict[str, Any]:
    """Waits for Component Ready & rollout, checks zero drift, strips keep, and probes gateway."""
    validate_dns1123_namespace(ns)
    if not skip_probe and not metrics_url:
        raise HandledError(
            "Metrics endpoint verification requires --metrics-url (or explicit --skip-probe).",
            f"Port-forward service/{GATEWAY_SERVICE_NAME} and pass --metrics-url http://127.0.0.1:8080/api/v1/metrics.",
        )
    _wait_for_component_ready(ns, timeout_sec)
    has_hpa, expected_replicas = _verify_adopted_objects_and_drift(ns)
    active_pods = _verify_rollout_and_pods(ns, timeout_sec, expected_replicas)
    stripped, cleaned_np = _strip_keep_and_cleanup_netpol(release, ns, has_hpa=has_hpa)
    probe_status = None if skip_probe else _probe_metrics_endpoint(metrics_url, test_xid)
    return {
        "namespace": ns,
        "component_release": GATEWAY_COMPONENT_RELEASE,
        "active_pods": active_pods,
        "stripped_keep": stripped,
        "legacy_netpol_deleted": cleaned_np,
        "probe_http_status": probe_status,
    }
