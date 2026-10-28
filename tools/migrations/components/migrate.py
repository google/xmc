#!/usr/bin/env python3
# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""One-time migration CLI from legacy umbrella metrics workloads to XMC Components."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

if str(_ROOT := Path(__file__).resolve().parents[3]) not in sys.path:
    sys.path.insert(0, str(_ROOT))

from tools.common.tool_utils import (  # noqa: E402
    HandledError,
    parse_bool_flag,
    run_command,
    run_tool,
    verify_helm_min_version,
)

COMPONENT_RELEASE = "xmc-cmp-ingestion-gateway"
PROFILE_RELEASE = "xmc-profile"
DEFAULT_PROFILE_CHART = _ROOT / "deploy" / "profiles" / "stable-2026a"
PRIMARY_WORKLOADS = ("deployment/ingestion-gateway-deployment", "service/ingestion-gateway-service")
HPA_RESOURCE = "horizontalpodautoscaler/ingestion-gateway-hpa"
COMPONENT_RESOURCE = "components.xmc.dev/ingestion-gateway"
COMPONENT_NETPOL = "networkpolicy/ingestion-gateway-network-policy"
_NAMESPACE_RE = re.compile(r"^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\Z")
_RELEASE_RE = re.compile(r"^[a-z0-9]([-.a-z0-9]{0,51}[a-z0-9])?\Z")
_PLACEHOLDER = "YOUR_GCP_PROJECT_ID"
_ABSENT = ("notfound", "not found", "doesn't have a resource type", "no matches for kind")


def _validate_identifiers(ns: str, rel: str) -> None:
    """Validates Kubernetes namespace and legacy umbrella Helm release names."""
    if not _NAMESPACE_RE.match(ns):
        raise HandledError(f"Invalid namespace '{ns}'.", "Pass a valid DNS-1123 label.")
    if not _RELEASE_RE.match(rel) or rel.startswith("xmc-cmp-"):
        raise HandledError(f"Invalid release '{rel}'.", "Pass a non-'xmc-cmp-*' release.")


def _mapping(parent: Mapping[str, Any], key: str) -> dict[str, Any]:
    """Returns a shallow dict copy for parent[key] when it is a mapping, else {}."""
    val = parent.get(key)
    return dict(val) if isinstance(val, Mapping) else {}


def _opt_str(val: Any, key: str) -> str:
    """Extracts a stripped string from a scalar Helm value, rejecting bools and containers."""
    if val is None or (isinstance(val, (str, int, float)) and not isinstance(val, bool)):
        return "" if val is None else str(val).strip()
    raise HandledError(f"Invalid scalar value for '{key}': {val!r}.", "Check Helm values.")


def _opt_int(val: Any, key: str) -> int | None:
    """Extracts an optional integer from a Helm value, rejecting bools, floats, and bad strings."""
    if not isinstance(val, float) and re.fullmatch(r"([+-]?[0-9]+)?", raw := _opt_str(val, key)):
        return int(raw) if raw else None
    raise HandledError(f"Invalid integer value for '{key}': {val!r}.", "Check Helm values.")


def _loads_obj(raw: str | None, src: str, *, null_ok: bool = False) -> dict[str, Any]:
    """Parses CLI stdout as a JSON object, raising HandledError on malformed payloads."""
    try:
        val = json.loads(raw or "{}")
    except json.JSONDecodeError as exc:
        raise HandledError(f"Malformed JSON from {src}.", "Inspect CLI stdout.") from exc
    if isinstance(val, Mapping) or (val is None and null_ok):
        return dict(val or {})
    raise HandledError(f"Expected JSON object from {src}.", "Inspect CLI stdout.")


def _exec(cmd: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Executes an external CLI command and wraps OS or subprocess failures in HandledError."""
    kw: dict[str, Any] = {"capture_output": True, "log_on_error": False, "log_cmd": False}
    try:
        return run_command(cmd, check=check, stream=sys.stderr, **kw)
    except subprocess.CalledProcessError as exc:
        err = (exc.stderr or exc.stdout or "").strip()
        info = {"cmd": cmd, "returncode": exc.returncode, "stderr": err}
        raise HandledError(f"Command failed: {' '.join(cmd)}", "Inspect stderr.", info) from exc
    except OSError as exc:
        raise HandledError(f"Cannot run '{cmd[0]}': {exc}", f"Install '{cmd[0]}' on PATH.") from exc


def _annotate(ns: str, resource: str, *specs: str) -> None:
    """Applies or removes Kubernetes annotations on a single namespaced resource."""
    _exec(["kubectl", "annotate", "--overwrite", "-n", ns, "--", resource, *specs])


def _set_helm_owner(ns: str, resource: str, owner: str, *, keep: bool) -> None:
    """Sets Helm release ownership annotations and sets or removes resource-policy=keep."""
    pol = "helm.sh/resource-policy=keep" if keep else "helm.sh/resource-policy-"
    rel_a, ns_a = f"meta.helm.sh/release-name={owner}", f"meta.helm.sh/release-namespace={ns}"
    _annotate(ns, resource, pol, rel_a, ns_a)


def _get_k8s_json(ns: str, resource: str) -> dict[str, Any] | None:
    """Fetches a Kubernetes resource as JSON, returning None when absent."""
    res = _exec(["kubectl", "get", "-n", ns, "-o", "json", "--", resource], check=False)
    if res.returncode != 0:
        err = (res.stderr or res.stdout or "").strip()
        if any(m in err.lower() for m in _ABSENT):
            return None
        msg = f"Cannot inspect '{resource}'."
        raise HandledError(msg, "Check KUBECONFIG/RBAC.", {"stderr": err})
    return _loads_obj(res.stdout, f"kubectl get {resource}")


def _get_helm_values(ns: str, rel: str) -> dict[str, Any]:
    """Returns merged Helm values (`helm get values -a`) for an existing release."""
    out = _exec(["helm", "get", "values", "-a", "-n", ns, "-o", "json", "--", rel]).stdout
    return _loads_obj(out, f"helm get values {rel}", null_ok=True)


def _build_redpanda_values(ig: Mapping[str, Any], *, enabled: bool) -> dict[str, Any]:
    """Builds stable-2026a components.redpanda overrides from umbrella gateway values."""
    params: dict[str, Any] = {}
    if brokers := _opt_str(ig.get("redpandaBrokers"), "redpandaBrokers"):
        params["brokerEndpoints"] = brokers
        if port_m := re.search(r"(?:\]:|^[^:\[\]]+:)([0-9]+)\Z", brokers.split(",")[0].strip()):
            params["kafkaPort"] = int(port_m.group(1))
    if topic := _opt_str(ig.get("topicName"), "topicName"):
        params["defaultTopic"] = topic
    return {"enabled": enabled, **({"parameters": params} if params else {})}


def _build_clickhouse_values(ch: Mapping[str, Any], *, enabled: bool) -> dict[str, Any]:
    """Builds stable-2026a components.clickhouse overrides from umbrella metrics values."""
    params: dict[str, Any] = {}
    if (port := _opt_int(ch.get("port"), "clickhouse.port")) is not None:
        params["nativePort"] = port
    for k in ("host", "database", "user", "secretName"):
        if val := _opt_str(ch.get(k), f"clickhouse.{k}"):
            params["defaultDatabase" if k == "database" else k] = val
    return {"enabled": enabled, **({"parameters": params} if params else {})}


def _apply_gateway_scaling_and_security(ig: Mapping[str, Any], params: dict[str, Any]) -> None:
    """Populates HPA/replicas, resource requests/limits, SASL, and TLS gateway parameters."""
    hpa = _mapping(ig, "hpa")
    params["hpaEnabled"] = bool(hpa) and parse_bool_flag(hpa.get("enabled", True))
    if not params["hpaEnabled"]:
        if (replicas := _opt_int(ig.get("replicas"), "replicas")) is not None:
            params["replicas"] = replicas
    else:
        for src_k, dst_k in (("minReplicas", "hpaMinReplicas"), ("maxReplicas", "hpaMaxReplicas")):
            if (bound := _opt_int(hpa.get(src_k), f"hpa.{src_k}")) is not None:
                params[dst_k] = bound
    res = _mapping(ig, "resources")
    for src, prefix in ((_mapping(res, "requests"), "Request"), (_mapping(res, "limits"), "Limit")):
        for res_k in ("cpu", "memory"):
            if val := _opt_str(src.get(res_k), f"resources.{res_k}"):
                params[f"{res_k}{prefix}"] = val
    for key in ("sasl", "tls"):
        cfg = _mapping(ig, key)
        sec = _opt_str(cfg.get("secretName"), f"{key}.secretName")
        on = parse_bool_flag(cfg.get("enabled", False))
        if on or sec:
            params[f"{key}Enabled"] = on
            if key == "sasl":
                mech = _opt_str(cfg.get("mechanism"), "sasl.mechanism")
                params["saslMechanism"] = mech or "SCRAM-SHA-256"
            if sec:
                params[f"{key}SecretName"] = sec


def _build_gateway_values(
    values: Mapping[str, Any], ig: Mapping[str, Any], *, enabled: bool
) -> dict[str, Any]:
    """Builds stable-2026a components.ingestion-gateway overrides from umbrella values."""
    params: dict[str, Any] = {}
    for k in ("port", "minPartitions", "minReplicas"):
        if (iv := _opt_int(ig.get(k), k)) is not None:
            params[k] = iv
    for k in ("redpandaBrokers", "topicName", "serviceAccountName"):
        if sv := _opt_str(ig.get(k), k):
            params[k] = sv
    if (proj := _opt_str(values.get("projectId"), "projectId")) and _PLACEHOLDER not in proj:
        params["projectId"] = proj
    fb_img = _opt_str(values.get("ingestion_gateway_custom_image"), "custom_image")
    if (img := _opt_str(ig.get("image"), "image") or fb_img) and _PLACEHOLDER not in img:
        params["image"] = img
    _apply_gateway_scaling_and_security(ig, params)
    tag = _opt_str(values.get("imageTag"), "imageTag")
    out: dict[str, Any] = {"enabled": enabled, **({"imageTag": tag} if tag else {})}
    return {**out, **({"parameters": params} if params else {})}


def _serialize_yaml(doc: Mapping[str, Any]) -> str:
    """Serializes profile values as block YAML when PyYAML is available, else YAML-1.2 JSON."""
    try:
        import yaml

        return str(yaml.safe_dump(dict(doc), sort_keys=False))
    except ImportError:
        return json.dumps(doc, indent=2) + "\n"


def run_prepare(args: argparse.Namespace) -> dict[str, Any]:
    """Generates and schema-validates profile-values.yaml from legacy umbrella Helm values."""
    ns, rel = args.namespace, args.release
    _validate_identifiers(ns, rel)
    try:
        verify_helm_min_version(stream=sys.stderr)
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as exc:
        raise HandledError(str(exc), "Install Helm >= 3.14.0 before running migration.") from exc
    values = _get_helm_values(ns, rel)
    ig, metrics = _mapping(values, "ingestionGatewayDeployment"), _mapping(values, "metrics")
    ig_on = parse_bool_flag(ig.get("enabled", False))
    ch_on = parse_bool_flag(metrics.get("enabled", False)) or ig_on
    comps = {
        "redpanda": _build_redpanda_values(ig, enabled=ig_on),
        "clickhouse": _build_clickhouse_values(_mapping(metrics, "clickhouse"), enabled=ch_on),
        "ingestion-gateway": _build_gateway_values(values, ig, enabled=ig_on),
    }
    out_path, chart = Path(args.output), str(args.profile_chart)
    try:
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(_serialize_yaml({"components": comps}), encoding="utf-8")
    except OSError as exc:
        raise HandledError(f"Cannot write '{out_path}': {exc}", "Check permissions.") from exc
    try:
        _exec(["helm", "template", "-n", ns, f"--values={out_path}", "--", PROFILE_RELEASE, chart])
    except HandledError:
        out_path.unlink(missing_ok=True)
        raise
    return {"command": "prepare", "output": str(out_path), "ingestion_gateway_enabled": ig_on}


def _verify_helm_owner(obj: Mapping[str, Any], resource: str, ns: str, allowed: set[str]) -> None:
    """Verifies that a live Kubernetes object is managed by Helm under an expected release."""
    meta = _mapping(obj, "metadata")
    managed_by = _mapping(meta, "labels").get("app.kubernetes.io/managed-by")
    ann = _mapping(meta, "annotations")
    rel, rel_ns = ann.get("meta.helm.sh/release-name"), ann.get("meta.helm.sh/release-namespace")
    if managed_by != "Helm" or rel not in allowed or rel_ns != ns:
        msg = f"Unexpected Helm owner on '{resource}' (release={rel!r}, ns={rel_ns!r})."
        raise HandledError(msg, f"Ensure '{resource}' is managed by {sorted(allowed)} in '{ns}'.")


def _collect_gateway_workloads(ns: str, allowed: set[str]) -> list[str]:
    """Discovers live ingestion-gateway workloads and validates their Helm ownership."""
    all_res = (*PRIMARY_WORKLOADS, HPA_RESOURCE)
    found = {r: o for r in all_res if (o := _get_k8s_json(ns, r)) is not None}
    present = [r for r in PRIMARY_WORKLOADS if r in found]
    if len(present) == 1:
        missing = next(r for r in PRIMARY_WORKLOADS if r not in found)
        raise HandledError(f"Partial gateway state: missing '{missing}'.", "Restore workloads.")
    workloads = [*present, *([HPA_RESOURCE] if present and HPA_RESOURCE in found else [])]
    for resource in workloads:
        _verify_helm_owner(found[resource], resource, ns, allowed)
    return workloads


def _undo_handoff(ns: str, rel: str) -> dict[str, Any]:
    """Reverts Helm ownership annotations back to the legacy umbrella release."""
    if _get_k8s_json(ns, COMPONENT_RESOURCE) is not None:
        hint = "Uninstall xmc-profile with deletionPolicy=Orphan and deletionProtection=false."
        raise HandledError(f"Cannot undo while '{COMPONENT_RESOURCE}' exists in '{ns}'.", hint)
    umbrella_vals = _get_helm_values(ns, rel)
    workloads = _collect_gateway_workloads(ns, {rel, COMPONENT_RELEASE})
    cmd = ["helm", "status", "-n", ns, "-o", "json", "--", COMPONENT_RELEASE]
    cmp_res = _exec(cmd, check=False)
    if uninstalled := (cmp_res.returncode == 0):
        sel = f"owner=helm,name={COMPONENT_RELEASE}"
        _exec(["kubectl", "delete", "secret", "--ignore-not-found", "-n", ns, "-l", sel])
    elif "not found" not in (cmp_res.stderr or cmp_res.stdout or "").lower():
        raise HandledError(f"Failed to check '{COMPONENT_RELEASE}' status.", "Verify Helm access.")
    for resource in workloads:
        _set_helm_owner(ns, resource, rel, keep=False)
    netpol = f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    cmp_on = parse_bool_flag(_mapping(umbrella_vals, "componentsDeployment").get("enabled", False))
    if not cmp_on and _get_k8s_json(ns, netpol) is not None:
        _annotate(ns, netpol, "helm.sh/resource-policy-")
    return {"command": "handoff", "undo": True, "uninstalled_component_release": uninstalled}


def run_handoff(args: argparse.Namespace) -> dict[str, Any]:
    """Transfers Helm ownership of live gateway workloads to xmc-cmp-ingestion-gateway."""
    ns, rel = args.namespace, args.release
    _validate_identifiers(ns, rel)
    if args.undo:
        return _undo_handoff(ns, rel)
    _get_helm_values(ns, rel)
    workloads = _collect_gateway_workloads(ns, {rel, COMPONENT_RELEASE})
    for resource in workloads:
        _set_helm_owner(ns, resource, COMPONENT_RELEASE, keep=True)
    netpol = f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    if (netpol_obj := _get_k8s_json(ns, netpol)) is not None:
        _verify_helm_owner(netpol_obj, netpol, ns, {rel})
        _annotate(ns, netpol, "helm.sh/resource-policy=keep")
    return {"command": "handoff", "undo": False, "transferred": workloads, "skipped": not workloads}


def _finalize_workloads(ns: str, *, hpa_on: bool) -> list[str]:
    """Verifies xmc-cmp-ingestion-gateway ownership, strips keep, and prunes retired HPA."""
    expected = [*PRIMARY_WORKLOADS, *([HPA_RESOURCE] if hpa_on else [])]
    for resource in expected:
        if (obj := _get_k8s_json(ns, resource)) is None:
            raise HandledError(f"Missing '{resource}'.", "Check xmc-bridge logs.")
        _verify_helm_owner(obj, resource, ns, {COMPONENT_RELEASE})
        _annotate(ns, resource, "helm.sh/resource-policy-")
    if not hpa_on and (hpa_obj := _get_k8s_json(ns, HPA_RESOURCE)) is not None:
        _verify_helm_owner(hpa_obj, HPA_RESOURCE, ns, {COMPONENT_RELEASE})
        _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", HPA_RESOURCE])
    return expected


def run_finalize(args: argparse.Namespace) -> dict[str, Any]:
    """Waits for Component readiness, strips temporary keep policies, and retires NetworkPolicy."""
    ns, rel = args.namespace, args.release
    _validate_identifiers(ns, rel)
    if args.timeout_seconds <= 0:
        raise HandledError("--timeout-seconds must be positive.", "Pass a positive integer.")
    if (comp := _get_k8s_json(ns, COMPONENT_RESOURCE)) is None:
        raise HandledError(f"Missing '{COMPONENT_RESOURCE}' in '{ns}'.", "Install xmc-profile.")
    spec = _mapping(comp, "spec")
    legacy_np = f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    if not parse_bool_flag(spec.get("enabled", False)):
        if _get_k8s_json(ns, PRIMARY_WORKLOADS[0]) is not None:
            raise HandledError("Gateway pods exist while Component is disabled.", "Enable it.")
        _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", legacy_np])
        return {"command": "finalize", "ingestion_gateway_enabled": False}
    wait = f"--timeout={args.timeout_seconds}s"
    for cond in ("Ready", "Synced"):
        flag = f"--for=condition={cond}"
        _exec(["kubectl", "wait", "-n", ns, wait, flag, "--", COMPONENT_RESOURCE])
    _exec(["kubectl", "rollout", "status", "-n", ns, wait, "--", PRIMARY_WORKLOADS[0]])
    st_out = _exec(["helm", "status", "-n", ns, "-o", "json", "--", COMPONENT_RELEASE]).stdout
    if (st := _mapping(_loads_obj(st_out, "helm status"), "info").get("status")) != "deployed":
        raise HandledError(f"Release '{COMPONENT_RELEASE}' is '{st}'.", "Inspect xmc-bridge logs.")
    hpa_s = "" if (v := _mapping(spec, "parameters").get("hpaEnabled")) is None else str(v).strip()
    finalized = _finalize_workloads(ns, hpa_on=hpa_s.lower() in ("", "true", "1"))
    if _get_k8s_json(ns, COMPONENT_NETPOL) is None:
        raise HandledError(f"Missing '{COMPONENT_NETPOL}'.", "Keep legacy NetworkPolicy.")
    _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", legacy_np])
    return {"command": "finalize", "ingestion_gateway_enabled": True, "finalized": finalized}


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser for the component migration tool."""
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="subcommand", required=True)
    shared = argparse.ArgumentParser(add_help=False)
    shared.add_argument("-n", "--namespace", default="xmc", help="Target Kubernetes namespace.")
    shared.add_argument("-r", "--release", default="xmc", help="Legacy umbrella release name.")
    prep = sub.add_parser("prepare", parents=[shared], help="Generate profile-values.yaml.")
    prep.add_argument("-o", "--output", default="profile-values.yaml", help="Output YAML path.")
    prep.add_argument("--profile-chart", default=str(DEFAULT_PROFILE_CHART), help="Chart path.")
    prep.set_defaults(handler=run_prepare)
    ho = sub.add_parser("handoff", parents=[shared], help="Transfer or undo workload ownership.")
    ho.add_argument("--undo", action="store_true", help="Revert ownership to umbrella release.")
    ho.set_defaults(handler=run_handoff)
    fin = sub.add_parser("finalize", parents=[shared], help="Finalize cutover after readiness.")
    fin.add_argument("--timeout-seconds", type=int, default=180, help="Wait timeout in seconds.")
    fin.set_defaults(handler=run_finalize)
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), lambda a: dict(a.handler(a)))
