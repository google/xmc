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

if str(_ROOT := Path(__file__).absolute().parents[3]) not in sys.path:
    sys.path.insert(0, str(_ROOT))

from tools.common import tool_utils as tu  # noqa: E402

COMPONENT_RELEASE = "xmc-cmp-ingestion-gateway"
DEFAULT_PROFILE_CHART = _ROOT / "deploy" / "profiles" / "stable-2026a"
PRIMARY_WORKLOADS = ("deployment/ingestion-gateway-deployment", "service/ingestion-gateway-service")
HPA_RESOURCE = "horizontalpodautoscaler/ingestion-gateway-hpa"
COMPONENT_RESOURCE = "components.xmc.dev/ingestion-gateway"


def _validate_identifiers(ns: str, rel: str) -> None:
    """Validates Kubernetes namespace and legacy umbrella Helm release names."""
    if not re.match(r"^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\Z", ns):
        raise tu.HandledError(f"Invalid namespace '{ns}'.", "Pass a valid DNS-1123 label.")
    if not re.match(r"^[a-z0-9]([-.a-z0-9]{0,51}[a-z0-9])?\Z", rel) or rel.startswith("xmc-cmp-"):
        raise tu.HandledError(f"Invalid release '{rel}'.", "Pass a non-'xmc-cmp-*' release.")


def _verify_target_codebase(chart: Path = DEFAULT_PROFILE_CHART, desc: str | None = None) -> None:
    """Verifies local repository templates and git tag match XMC 0.18.x."""
    gw_tpl = chart / "components/ingestion-gateway/templates/deployment.yaml"
    legacy_ok = (_ROOT / "deploy/helm/templates/ingestion-gateway/deployment.yaml").is_file()
    if not legacy_ok or not gw_tpl.is_file() or "hpaEnabled" not in gw_tpl.read_text("utf-8"):
        raise tu.HandledError("Unsupported target codebase.", "Checkout XMC 0.18.x.")
    if desc is None:
        cmd = ["git", "-C", str(_ROOT), "describe", "--tags", "--match", "[0-9]*.[0-9]*.[0-9]*"]
        try:
            res = subprocess.run(cmd, check=False, text=True, capture_output=True)
            desc = res.stdout.strip() if res.returncode == 0 else ""
        except OSError:
            desc = ""
    if m := re.match(r"^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9]+)-g[0-9a-f]+)?\Z", desc):
        ver = (int(m.group(1)), int(m.group(2)), int(m.group(3)))
        min_v = (0, 17, 0) if int(m.group(4) or 0) > 0 else (0, 18, 0)
        if not (min_v <= ver < (0, 19, 0)):
            raise tu.HandledError(f"Unsupported target XMC '{desc}'.", "Checkout 0.18.x.")


def _verify_source_env(vals: Mapping[str, Any], *, require_legacy: bool) -> None:
    """Verifies live cluster Helm release state and SemVer imageTag match a valid source."""
    if require_legacy and tu.parse_bool_flag(_mapping(vals, "componentsDeployment").get("enabled")):
        raise tu.HandledError("componentsDeployment.enabled is already true.", "Use legacy chart.")
    tag = _opt_str(vals.get("imageTag"), "imageTag")
    if (m := re.match(r"^v?(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?\Z", tag)) and int(
        m[1]
    ) < 2000:
        ver = (int(m.group(1)), int(m.group(2)), int(m.group(3)))
        ig_on = tu.parse_bool_flag(_mapping(vals, "ingestionGatewayDeployment").get("enabled"))
        min_v = (0, 4, 0) if ig_on else (0, 0, 0)
        if not (min_v <= ver < (0, 19, 0)):
            raise tu.HandledError(f"Unsupported source imageTag '{tag}'.", "Check XMC version.")


def _mapping(parent: Mapping[str, Any], key: str) -> dict[str, Any]:
    """Returns a shallow dict copy for parent[key] when it is a mapping, else {}."""
    return dict(val) if isinstance(val := parent.get(key), Mapping) else {}


def _opt_str(val: Any, key: str) -> str:
    """Extracts a stripped string from a scalar Helm value, rejecting bools and containers."""
    if val is None or (isinstance(val, (str, int, float)) and not isinstance(val, bool)):
        return "" if val is None else str(val).strip()
    raise tu.HandledError(f"Invalid scalar value for '{key}': {val!r}.", "Check Helm values.")


def _opt_int(val: Any, key: str) -> int | None:
    """Extracts an optional integer from a Helm value, rejecting bools, floats, and bad strings."""
    if not isinstance(val, float) and re.fullmatch(r"([+-]?[0-9]+)?", raw := _opt_str(val, key)):
        return int(raw) if raw else None
    raise tu.HandledError(f"Invalid integer value for '{key}': {val!r}.", "Check Helm values.")


def _loads_obj(raw: str | None, src: str, *, null_ok: bool = False) -> dict[str, Any]:
    """Parses CLI stdout as a JSON object, raising HandledError on malformed payloads."""
    try:
        val = json.loads(raw or "{}")
    except json.JSONDecodeError as exc:
        raise tu.HandledError(f"Malformed JSON from {src}.", "Inspect CLI stdout.") from exc
    if isinstance(val, Mapping) or (val is None and null_ok):
        return dict(val or {})
    raise tu.HandledError(f"Expected JSON object from {src}.", "Inspect CLI stdout.")


def _exec(cmd: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Executes an external CLI command and wraps OS or subprocess failures in HandledError."""
    kw: dict[str, Any] = {"capture_output": True, "log_on_error": False, "log_cmd": False}
    try:
        return tu.run_command(cmd, check=check, stream=sys.stderr, **kw)
    except (subprocess.CalledProcessError, OSError) as exc:
        err = (getattr(exc, "stderr", "") or getattr(exc, "stdout", "") or str(exc)).strip()
        info = {"cmd": cmd, "stderr": err}
        raise tu.HandledError(f"Command failed: {' '.join(cmd)}", "Inspect CLI.", info) from exc


def _annotate(ns: str, resource: str, *specs: str, owner: str = "") -> None:
    """Applies Kubernetes annotations (and optional Helm release ownership) to a resource."""
    rel = (f"meta.helm.sh/release-name={owner}", f"meta.helm.sh/release-namespace={ns}")
    extra = (*specs, *(rel if owner else ()))
    _exec(["kubectl", "annotate", "--overwrite", "-n", ns, "--", resource, *extra])


def _get_k8s_json(ns: str, resource: str) -> dict[str, Any] | None:
    """Fetches a Kubernetes resource as JSON, returning None when absent."""
    res = _exec(["kubectl", "get", "-n", ns, "-o", "json", "--", resource], check=False)
    if res.returncode == 0:
        return _loads_obj(res.stdout, f"kubectl get {resource}")
    err = (res.stderr or res.stdout or "").strip()
    absent = ("notfound", "not found", "doesn't have a resource type", "no matches for kind")
    if any(m in err.lower() for m in absent):
        return None
    raise tu.HandledError(f"Cannot inspect '{resource}'.", "Check KUBECONFIG.", {"stderr": err})


def _load_source_values(ns: str, rel: str, *, require_legacy: bool) -> dict[str, Any]:
    """Loads merged Helm values for an existing umbrella release and validates source bounds."""
    out = _exec(["helm", "get", "values", "-a", "-n", ns, "-o", "json", "--", rel]).stdout
    values = _loads_obj(out, f"helm get values {rel}", null_ok=True)
    _verify_source_env(values, require_legacy=require_legacy)
    return values


def _build_backing_components(
    ig: Mapping[str, Any], metrics: Mapping[str, Any], *, ig_on: bool
) -> dict[str, Any]:
    """Builds stable-2026a redpanda and clickhouse overrides from umbrella values."""
    b, t = _opt_str(ig.get("redpandaBrokers"), "brokers"), _opt_str(ig.get("topicName"), "topic")
    pm = re.search(r"(?:\]:|^[^:\[\]]+:)([0-9]+)\Z", b.split(",")[0].strip()) if b else None
    rp: dict[str, Any] = {k: v for k, v in (("brokerEndpoints", b), ("defaultTopic", t)) if v}
    if pm:
        rp["kafkaPort"] = int(pm.group(1))
    ch_s = _mapping(metrics, "clickhouse")
    port = _opt_int(ch_s.get("port"), "clickhouse.port")
    ch: dict[str, Any] = {"nativePort": port} if port is not None else {}
    for k in ("host", "database", "user", "secretName"):
        if v := _opt_str(ch_s.get(k), f"clickhouse.{k}"):
            ch["defaultDatabase" if k == "database" else k] = v
    ch_on = tu.parse_bool_flag(metrics.get("enabled", False)) or ig_on
    rp_cfg = {"enabled": ig_on, **({"parameters": rp} if rp else {})}
    ch_cfg = {"enabled": ch_on, **({"parameters": ch} if ch else {})}
    return {"redpanda": rp_cfg, "clickhouse": ch_cfg}


def _build_gateway_values(
    values: Mapping[str, Any], ig: Mapping[str, Any], *, enabled: bool
) -> dict[str, Any]:
    """Builds stable-2026a components.ingestion-gateway overrides from umbrella values."""
    int_keys = ("port", "minPartitions", "minReplicas")
    str_keys = ("redpandaBrokers", "topicName", "serviceAccountName")
    params: dict[str, Any] = {k: iv for k in int_keys if (iv := _opt_int(ig.get(k), k)) is not None}
    params |= {k: sv for k in str_keys if (sv := _opt_str(ig.get(k), k))}
    ph = "YOUR_GCP_PROJECT_ID"
    fb_img = _opt_str(values.get("ingestion_gateway_custom_image"), "custom_image")
    if (proj := _opt_str(values.get("projectId"), "projectId")) and ph not in proj:
        params["projectId"] = proj
    if (img := _opt_str(ig.get("image"), "image") or fb_img) and ph not in img:
        params["image"] = img
    hpa, res = _mapping(ig, "hpa"), _mapping(ig, "resources")
    params["hpaEnabled"] = bool(hpa) and tu.parse_bool_flag(hpa.get("enabled", True))
    if not params["hpaEnabled"]:
        if (rep := _opt_int(ig.get("replicas"), "replicas")) is not None:
            params["replicas"] = rep
    else:
        bounds = (("minReplicas", "hpaMinReplicas"), ("maxReplicas", "hpaMaxReplicas"))
        params |= {d: b for s, d in bounds if (b := _opt_int(hpa.get(s), f"hpa.{s}")) is not None}
    for sec_name, suffix in (("requests", "Request"), ("limits", "Limit")):
        sm = _mapping(res, sec_name)
        params |= {f"{k}{suffix}": rv for k in ("cpu", "memory") if (rv := _opt_str(sm.get(k), k))}
    for key in ("sasl", "tls"):
        cfg, sec = _mapping(ig, key), _opt_str(_mapping(ig, key).get("secretName"), f"{key}.secret")
        if (on := tu.parse_bool_flag(cfg.get("enabled", False))) or sec:
            params[f"{key}Enabled"] = on
            if key == "sasl":
                mech = _opt_str(cfg.get("mechanism"), "sasl.mechanism")
                params["saslMechanism"] = mech or "SCRAM-SHA-256"
            if sec:
                params[f"{key}SecretName"] = sec
    tag = _opt_str(values.get("imageTag"), "imageTag")
    return {"enabled": enabled, **({"imageTag": tag} if tag else {}), "parameters": params}


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
    chart, out = Path(args.profile_chart), Path(args.output)
    _validate_identifiers(ns, rel)
    _verify_target_codebase(chart)
    try:
        tu.verify_helm_min_version()
        values = _load_source_values(ns, rel, require_legacy=True)
        ig, metrics = _mapping(values, "ingestionGatewayDeployment"), _mapping(values, "metrics")
        ig_on = tu.parse_bool_flag(ig.get("enabled", False))
        comps = _build_backing_components(ig, metrics, ig_on=ig_on)
        comps["ingestion-gateway"] = _build_gateway_values(values, ig, enabled=ig_on)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(_serialize_yaml({"components": comps}), encoding="utf-8")
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as exc:
        raise tu.HandledError(str(exc), "Verify Helm >= 3.14.0 and output permissions.") from exc
    try:
        _exec(["helm", "template", "-n", ns, f"--values={out}", "--", "xmc-profile", str(chart)])
    except tu.HandledError:
        out.unlink(missing_ok=True)
        raise
    return {"command": "prepare", "output": str(out), "ingestion_gateway_enabled": ig_on}


def _verify_helm_owner(obj: Mapping[str, Any], resource: str, ns: str, allowed: set[str]) -> None:
    """Verifies that a live Kubernetes object is managed by Helm under an expected release."""
    meta = _mapping(obj, "metadata")
    ann, lbl = _mapping(meta, "annotations"), _mapping(meta, "labels")
    rel, rel_ns = ann.get("meta.helm.sh/release-name"), ann.get("meta.helm.sh/release-namespace")
    if lbl.get("app.kubernetes.io/managed-by") != "Helm" or rel not in allowed or rel_ns != ns:
        msg = f"Unexpected Helm owner on '{resource}' (release={rel!r}, ns={rel_ns!r})."
        raise tu.HandledError(msg, f"Ensure '{resource}' is owned by {sorted(allowed)} in '{ns}'.")


def _collect_gateway_workloads(ns: str, allowed: set[str]) -> list[str]:
    """Discovers live ingestion-gateway workloads and validates their Helm ownership."""
    candidates = (*PRIMARY_WORKLOADS, HPA_RESOURCE)
    found = {r: o for r in candidates if (o := _get_k8s_json(ns, r)) is not None}
    present = [r for r in PRIMARY_WORKLOADS if r in found]
    if len(present) == 1:
        missing = next(r for r in PRIMARY_WORKLOADS if r not in found)
        raise tu.HandledError(f"Partial gateway state: missing '{missing}'.", "Restore workloads.")
    workloads = [*present, *([HPA_RESOURCE] if present and HPA_RESOURCE in found else [])]
    for resource in workloads:
        _verify_helm_owner(found[resource], resource, ns, allowed)
    return workloads


def _undo_handoff(ns: str, rel: str) -> dict[str, Any]:
    """Reverts Helm ownership annotations back to the legacy umbrella release."""
    if _get_k8s_json(ns, COMPONENT_RESOURCE) is not None:
        hint = "Uninstall xmc-profile with deletionPolicy=Orphan and deletionProtection=false."
        raise tu.HandledError(f"Cannot undo while '{COMPONENT_RESOURCE}' exists in '{ns}'.", hint)
    umbrella_vals = _load_source_values(ns, rel, require_legacy=False)
    workloads = _collect_gateway_workloads(ns, {rel, COMPONENT_RELEASE})
    cmp_res = _exec(["helm", "status", "-n", ns, "-o", "json", COMPONENT_RELEASE], check=False)
    if uninstalled := (cmp_res.returncode == 0):
        sel = f"owner=helm,name={COMPONENT_RELEASE}"
        _exec(["kubectl", "delete", "secret", "--ignore-not-found", "-n", ns, "-l", sel])
    elif "not found" not in (cmp_res.stderr or cmp_res.stdout or "").lower():
        raise tu.HandledError(f"Failed to check '{COMPONENT_RELEASE}' status.", "Check Helm.")
    for resource in workloads:
        _annotate(ns, resource, "helm.sh/resource-policy-", owner=rel)
    netpol = f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    cmp_on = tu.parse_bool_flag(_mapping(umbrella_vals, "componentsDeployment").get("enabled"))
    if not cmp_on and _get_k8s_json(ns, netpol) is not None:
        _annotate(ns, netpol, "helm.sh/resource-policy-")
    return {"command": "handoff", "undo": True, "uninstalled_component_release": uninstalled}


def run_handoff(args: argparse.Namespace) -> dict[str, Any]:
    """Transfers Helm ownership of live gateway workloads to xmc-cmp-ingestion-gateway."""
    ns, rel = args.namespace, args.release
    _validate_identifiers(ns, rel)
    _verify_target_codebase()
    if args.undo:
        return _undo_handoff(ns, rel)
    _load_source_values(ns, rel, require_legacy=True)
    workloads = _collect_gateway_workloads(ns, {rel, COMPONENT_RELEASE})
    for resource in workloads:
        _annotate(ns, resource, "helm.sh/resource-policy=keep", owner=COMPONENT_RELEASE)
    netpol = f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    if (netpol_obj := _get_k8s_json(ns, netpol)) is not None:
        _verify_helm_owner(netpol_obj, netpol, ns, {rel})
        _annotate(ns, netpol, "helm.sh/resource-policy=keep")
    return {"command": "handoff", "undo": False, "transferred": workloads, "skipped": not workloads}


def run_finalize(args: argparse.Namespace) -> dict[str, Any]:
    """Waits for Component readiness, strips temporary keep policies, and retires NetworkPolicy."""
    ns, rel = args.namespace, args.release
    _validate_identifiers(ns, rel)
    _verify_target_codebase()
    if args.timeout_seconds <= 0:
        raise tu.HandledError("--timeout-seconds must be positive.", "Pass a positive integer.")
    if (comp := _get_k8s_json(ns, COMPONENT_RESOURCE)) is None:
        raise tu.HandledError(f"Missing '{COMPONENT_RESOURCE}' in '{ns}'.", "Install xmc-profile.")
    spec, np = _mapping(comp, "spec"), f"networkpolicy/{rel}-ingestion-gateway-network-policy"
    if not tu.parse_bool_flag(spec.get("enabled", False)):
        if _get_k8s_json(ns, PRIMARY_WORKLOADS[0]) is not None:
            raise tu.HandledError("Gateway pods exist while Component is disabled.", "Enable it.")
        _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", np])
        return {"command": "finalize", "ingestion_gateway_enabled": False}
    wait, cr = f"--timeout={args.timeout_seconds}s", COMPONENT_RESOURCE
    for cond in ("Ready", "Synced"):
        _exec(["kubectl", "wait", "-n", ns, wait, f"--for=condition={cond}", "--", cr])
    _exec(["kubectl", "rollout", "status", "-n", ns, wait, "--", PRIMARY_WORKLOADS[0]])
    st_out = _exec(["helm", "status", "-n", ns, "-o", "json", "--", COMPONENT_RELEASE]).stdout
    if (st := _mapping(_loads_obj(st_out, "helm status"), "info").get("status")) != "deployed":
        raise tu.HandledError(f"Release '{COMPONENT_RELEASE}' is '{st}'.", "Inspect bridge logs.")
    hpa_s = "" if (v := _mapping(spec, "parameters").get("hpaEnabled")) is None else str(v).strip()
    hpa_on = hpa_s.lower() in ("", "true", "1")
    expected = [*PRIMARY_WORKLOADS, *([HPA_RESOURCE] if hpa_on else [])]
    for resource in expected:
        if (obj := _get_k8s_json(ns, resource)) is None:
            raise tu.HandledError(f"Missing '{resource}'.", "Check xmc-bridge logs.")
        _verify_helm_owner(obj, resource, ns, {COMPONENT_RELEASE})
        _annotate(ns, resource, "helm.sh/resource-policy-")
    if not hpa_on and (hpa_obj := _get_k8s_json(ns, HPA_RESOURCE)) is not None:
        _verify_helm_owner(hpa_obj, HPA_RESOURCE, ns, {COMPONENT_RELEASE})
        _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", HPA_RESOURCE])
    if _get_k8s_json(ns, "networkpolicy/ingestion-gateway-network-policy") is None:
        raise tu.HandledError("Missing component NetworkPolicy.", "Keep legacy NetworkPolicy.")
    _exec(["kubectl", "delete", "--ignore-not-found", "-n", ns, "--", np])
    return {"command": "finalize", "ingestion_gateway_enabled": True, "finalized": expected}


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser for the component migration tool."""
    parser = argparse.ArgumentParser(description=__doc__)
    shared = argparse.ArgumentParser(add_help=False)
    for flags in (("-n", "--namespace"), ("-r", "--release")):
        shared.add_argument(*flags, default="xmc")
    sub = parser.add_subparsers(dest="subcommand", required=True)
    (prep := sub.add_parser("prepare", parents=[shared])).set_defaults(handler=run_prepare)
    prep.add_argument("-o", "--output", default="profile-values.yaml")
    prep.add_argument("--profile-chart", default=str(DEFAULT_PROFILE_CHART))
    (ho := sub.add_parser("handoff", parents=[shared])).set_defaults(handler=run_handoff)
    ho.add_argument("--undo", action="store_true")
    (fin := sub.add_parser("finalize", parents=[shared])).set_defaults(handler=run_finalize)
    fin.add_argument("--timeout-seconds", type=int, default=180)
    return parser


def _confirm_and_run(args: argparse.Namespace) -> dict[str, Any]:
    """Prompts on an interactive TTY before running the selected migration subcommand."""
    if not sys.stdin.isatty():
        raise tu.HandledError("Interactive TTY confirmation required.", "Run manually in a TTY.")
    msg = f"WARNING: XMC 0.18.x '{args.subcommand}' ({args.namespace}/{args.release})."
    sys.stderr.write(f"{msg} Type 'yes' to confirm: ")
    sys.stderr.flush()
    if sys.stdin.readline().strip().lower() != "yes":
        raise tu.HandledError("Migration cancelled by user.", "Type 'yes' to proceed.")
    return dict(args.handler(args))


if __name__ == "__main__":
    tu.run_tool(build_parser(), _confirm_and_run)
