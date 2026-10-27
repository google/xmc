# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Builds stable-2026a profile values from a legacy umbrella Ingestion Gateway install."""

import json
import math
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, parse_bool_flag, verify_helm_min_version
from tools.migrations.components.cluster_inspector import (
    GATEWAY_DEPLOYMENT_NAME,
    GATEWAY_HPA_NAME,
    fetch_live_resource,
    fetch_umbrella_values,
)

_COMPONENTS = ("redpanda", "clickhouse", "ingestion-gateway")
_DEFAULT_BROKERS = "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
_DNS1123_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?\Z")
_BOOL_FLAGS = ("enable_components", "sasl_enabled", "tls_enabled", "tls_insecure_skip_verify")


def _subdict(mapping: Any, key: str) -> dict[str, Any]:
    """Returns mapping[key] when both mapping and mapping[key] are dicts, else {}."""
    val = mapping.get(key) if isinstance(mapping, dict) else None
    return val if isinstance(val, dict) else {}


def _pod_spec(deployment: dict[str, Any]) -> dict[str, Any]:
    """Returns the PodSpec mapping from a Deployment dictionary."""
    return _subdict(_subdict(_subdict(deployment, "spec"), "template"), "spec")


def _primary_container(deployment: dict[str, Any]) -> dict[str, Any]:
    """Returns the ingestion-gateway container spec (or first container fallback)."""
    raw = _pod_spec(deployment).get("containers")
    cs = [c for c in raw if isinstance(c, dict)] if isinstance(raw, list) else []
    return next((c for c in cs if c.get("name") == "ingestion-gateway"), cs[0] if cs else {})


def _resolve_int(*candidates: Any, default: int, field: str) -> int:
    """Returns the first defined integer candidate (preserving 0), or default."""
    for val in candidates:
        if val is not None and val != "" and not isinstance(val, bool):
            try:
                return int(val)
            except (TypeError, ValueError) as e:
                raise HandledError(f"Invalid integer for {field}: {val!r}", f"Fix {field}.") from e
    return default


def parse_gateway_cli_flags(deployment: dict[str, Any]) -> dict[str, str]:
    """Extracts --key=value, -key value, and bare bool CLI flags from ingestion-gateway."""
    parsed: dict[str, str] = {}
    raw_args = _primary_container(deployment).get("args")
    args = raw_args if isinstance(raw_args, list) else []
    i = 0
    while i < len(args):
        arg, i = args[i], i + 1
        if arg == "--":
            break
        if not isinstance(arg, str) or not arg.startswith("-") or arg == "-":
            continue
        key, sep, val = (arg[2:] if arg.startswith("--") else arg[1:]).partition("=")
        if sep or key in _BOOL_FLAGS:
            parsed[key] = val if sep else "true"
        elif i < len(args) and isinstance(args[i], str) and not args[i].startswith("-"):
            parsed[key], i = args[i], i + 1
    return parsed


def build_gateway_parameters(
    umbrella_values: dict[str, Any],
    live_dep: dict[str, Any],
    live_hpa: dict[str, Any] | None,
    live_flags: dict[str, str],
) -> dict[str, Any]:
    """Maps live Deployment/HPA state and umbrella values into flat component parameters."""
    ig = _subdict(umbrella_values, "ingestionGatewayDeployment")
    pod_spec, container = _pod_spec(live_dep), _primary_container(live_dep)
    custom_img = str(umbrella_values.get("ingestion_gateway_custom_image") or "").strip()
    live_img = str(container.get("image") or "").strip()
    image = custom_img if custom_img and "YOUR_GCP_PROJECT_ID" not in custom_img else live_img
    if not image or "YOUR_GCP_PROJECT_ID" in image:
        raise HandledError("Could not resolve ingestion-gateway image.", "Set custom image first.")
    brokers = live_flags.get("redpanda_brokers") or ig.get("redpandaBrokers") or _DEFAULT_BROKERS
    topic = live_flags.get("topic_name") or ig.get("topicName") or "xmc-metrics"
    sa = pod_spec.get("serviceAccountName") or ig.get("serviceAccountName") or "default"
    params: dict[str, Any] = {
        "image": image,
        "port": _resolve_int(live_flags.get("port"), ig.get("port"), default=8080, field="port"),
        "redpandaBrokers": str(brokers),
        "topicName": str(topic),
        "minPartitions": _resolve_int(
            live_flags.get("min_partitions"), ig.get("minPartitions"), default=128, field="parts"
        ),
        "minReplicas": _resolve_int(
            live_flags.get("min_replicas"), ig.get("minReplicas"), default=1, field="minReplicas"
        ),
        "serviceAccountName": str(sa),
    }
    _apply_scaling_and_resources(params, ig, live_dep, live_hpa)
    return params


def _apply_scaling_and_resources(
    params: dict[str, Any], ig: dict[str, Any], dep: dict[str, Any], hpa: dict[str, Any] | None
) -> None:
    """Populates HPA/replica and per-field CPU/memory resource parameters."""
    hpa_cfg = _subdict(ig, "hpa")
    if hpa is not None:
        hs = _subdict(hpa, "spec")
        params["hpaEnabled"] = True
        params["hpaMinReplicas"] = _resolve_int(
            hs.get("minReplicas"), hpa_cfg.get("minReplicas"), default=3, field="hpaMinReplicas"
        )
        params["hpaMaxReplicas"] = _resolve_int(
            hs.get("maxReplicas"), hpa_cfg.get("maxReplicas"), default=10, field="hpaMaxReplicas"
        )
    else:
        params["hpaEnabled"] = False
        dep_rep = _subdict(dep, "spec").get("replicas")
        params["replicas"] = _resolve_int(dep_rep, ig.get("replicas"), default=1, field="replicas")
    c_res, ig_res = _subdict(_primary_container(dep), "resources"), _subdict(ig, "resources")
    c_req, ig_req = _subdict(c_res, "requests"), _subdict(ig_res, "requests")
    c_lim, ig_lim = _subdict(c_res, "limits"), _subdict(ig_res, "limits")
    for key, live_d, ig_d, f in (
        ("cpuRequest", c_req, ig_req, "cpu"),
        ("memoryRequest", c_req, ig_req, "memory"),
        ("cpuLimit", c_lim, ig_lim, "cpu"),
        ("memoryLimit", c_lim, ig_lim, "memory"),
    ):
        val = live_d.get(f) or ig_d.get(f)
        if val:
            params[key] = str(val)


def build_redpanda_parameters(gateway_params: dict[str, Any]) -> dict[str, Any]:
    """Prefills Redpanda 3P wrapper parameters from gateway broker and topic settings."""
    brokers = str(gateway_params.get("redpandaBrokers") or _DEFAULT_BROKERS)
    first = brokers.partition(",")[0].strip()
    if first.startswith("["):
        raw_port = first.partition("]:")[2]
    else:
        raw_port = first.partition(":")[2] if first.count(":") == 1 else ""
    port = int(raw_port) if raw_port.isascii() and raw_port.isdigit() else 9092
    return {
        "brokerEndpoints": brokers,
        "kafkaPort": port if 1 <= port <= 65535 else 9092,
        "defaultTopic": str(gateway_params.get("topicName") or "xmc-metrics"),
    }


def build_clickhouse_parameters(umbrella_values: dict[str, Any]) -> dict[str, Any]:
    """Prefills ClickHouse 3P wrapper parameters from umbrella metrics.clickhouse settings."""
    ch = _subdict(_subdict(umbrella_values, "metrics"), "clickhouse")
    host = str(ch.get("host") or "clickhouse.xmc-metrics.svc.cluster.local")
    port = _resolve_int(ch.get("port"), default=9000, field="metrics.clickhouse.port")
    ep_host = f"[{host}]" if ":" in host and not host.startswith("[") else host
    return {
        "host": host,
        "clusterEndpoints": f"{ep_host}:{port}",
        "nativePort": port,
        "httpPort": 8123,
        "defaultDatabase": str(ch.get("database") or "default"),
        "user": str(ch.get("user") or "default"),
        "secretName": str(ch.get("secretName") or "clickhouse-secret"),
        "secretPasswordKey": "clickhouse-password",
    }


def render_profile_yaml(doc: dict[str, Any], indent: int = 0) -> str:
    """Serializes nested string/scalar dicts into clean deterministic block YAML."""
    lines: list[str] = []
    pad = " " * indent
    for key, val in doc.items():
        if val is None:
            raise ValueError(f"Null value not permitted in profile YAML for key '{key}'")
        if isinstance(val, dict):
            sub = render_profile_yaml(val, indent + 2).rstrip("\n") if val else ""
            lines.extend((f"{pad}{key}:", sub) if val else (f"{pad}{key}: {{}}",))
        elif isinstance(val, bool):
            lines.append(f"{pad}{key}: {'true' if val else 'false'}")
        elif isinstance(val, int | float):
            if isinstance(val, float) and not math.isfinite(val):
                raise ValueError(f"Non-finite float not permitted for key '{key}'")
            lines.append(f"{pad}{key}: {val}")
        else:
            lines.append(f"{pad}{key}: {json.dumps(str(val))}")
    return "\n".join(lines) + "\n"


def prepare_profile_values(release: str, namespace: str, output_path: Path) -> dict[str, Any]:
    """Generates profile-values.yaml from live umbrella values and gateway state."""
    if not namespace or len(namespace) > 63 or not _DNS1123_RE.match(namespace):
        raise HandledError(f"Invalid Kubernetes namespace {namespace!r}.", "Use a DNS-1123 label.")
    try:
        helm_ver = verify_helm_min_version(stream=sys.stderr)
    except (FileNotFoundError, subprocess.CalledProcessError, RuntimeError, ValueError) as e:
        raise HandledError(str(e), "Upgrade Helm CLI to >= 3.14.0 before migrating.") from e
    umbrella_values = fetch_umbrella_values(release, namespace)
    ig_on = parse_bool_flag(_subdict(umbrella_values, "ingestionGatewayDeployment").get("enabled"))
    off = {"components": {n: {"enabled": False, "namespace": namespace} for n in _COMPONENTS}}
    doc = _build_enabled_profile_doc(umbrella_values, namespace) if ig_on else off
    rendered = render_profile_yaml(doc)
    try:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(rendered, encoding="utf-8")
    except OSError as e:
        raise HandledError(f"Failed to write '{output_path}': {e}", "Check --output path.") from e
    return {
        "output_path": str(output_path),
        "namespace": namespace,
        "umbrella_release": release,
        "gateway_enabled": ig_on,
        "helm_version": helm_ver,
    }


def _build_enabled_profile_doc(umbrella_values: dict[str, Any], namespace: str) -> dict[str, Any]:
    """Inspects live cluster objects and constructs the enabled profile dictionary."""
    live_dep = fetch_live_resource("deployment", GATEWAY_DEPLOYMENT_NAME, namespace, required=True)
    assert isinstance(live_dep, dict)
    live_hpa = fetch_live_resource(
        "horizontalpodautoscaler", GATEWAY_HPA_NAME, namespace, required=False
    )
    gw_params = build_gateway_parameters(
        umbrella_values, live_dep, live_hpa, parse_gateway_cli_flags(live_dep)
    )
    param_map = {
        "redpanda": build_redpanda_parameters(gw_params),
        "clickhouse": build_clickhouse_parameters(umbrella_values),
        "ingestion-gateway": gw_params,
    }
    return {
        "components": {
            n: {"enabled": True, "namespace": namespace, "parameters": p}
            for n, p in param_map.items()
        }
    }
