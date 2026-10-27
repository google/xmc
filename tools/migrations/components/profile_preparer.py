# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Builds and validates stable-2026a profile values from a legacy umbrella install."""

import json
import math
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

from tools.common.tool_utils import (
    HandledError,
    parse_bool_flag,
    run_command,
    verify_helm_min_version,
)
from tools.components.run_linter import LintTarget, resolve_linter_binary, run_linter_on_target
from tools.migrations.components.cluster_inspector import (
    GATEWAY_DEPLOYMENT_NAME,
    GATEWAY_HPA_NAME,
    fetch_live_resource,
    fetch_umbrella_values,
    validate_dns1123_namespace,
    verify_secret_exists,
)

_COMPONENTS = ("redpanda", "clickhouse", "ingestion-gateway")
_DEFAULT_BROKERS = "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
_BOOL_FLAGS = ("enable_components", "sasl_enabled", "tls_enabled", "tls_insecure_skip_verify")
_KNOWN_GATEWAY_FLAGS = frozenset(
    [
        "port",
        "enable_components",
        "components_namespace",
        "redpanda_brokers",
        "topic_name",
        "min_partitions",
        "min_replicas",
        "project_id",
        "sasl_enabled",
        "sasl_mechanism",
        "sasl_credentials_dir",
        "tls_enabled",
        "tls_ca_file",
        "tls_cert_file",
        "tls_key_file",
    ]
)
_KNOWN_VOLUME_NAMES = frozenset({"kafka-sasl-credentials", "kafka-tls-credentials"})
_FLAG_ERR_HINT = "Component gateway supports Redpanda ingestion flags only."


def _pod_spec(deployment: dict[str, Any]) -> dict[str, Any]:
    """Returns the PodSpec mapping from a Deployment dictionary."""
    return ((deployment.get("spec") or {}).get("template") or {}).get("spec") or {}


def _primary_container(deployment: dict[str, Any]) -> dict[str, Any]:
    """Returns the ingestion-gateway container spec (or first container fallback)."""
    containers: list[dict[str, Any]] = _pod_spec(deployment).get("containers") or []
    for c in containers:
        if c.get("name") == "ingestion-gateway":
            return c
    return containers[0] if containers else {}


def _resolve_int(*candidates: Any, default: int, field: str) -> int:
    """Returns the first defined integer candidate (preserving 0), or default."""
    for val in candidates:
        if val is not None and val != "" and not isinstance(val, bool):
            try:
                return int(val)
            except (TypeError, ValueError) as e:
                raise HandledError(f"Invalid integer for {field}: {val!r}", f"Fix {field}.") from e
    return default


def audit_live_gateway_deployment(deployment: dict[str, Any]) -> dict[str, str]:
    """Audits live gateway PodSpec for unmapped customizations and returns parsed CLI flags."""
    pod_spec = _pod_spec(deployment)
    if pod_spec.get("initContainers"):
        raise HandledError(
            "Live ingestion-gateway has custom initContainers.",
            "Migrate initContainers manually before enabling components.",
        )
    containers = pod_spec.get("containers") or []
    if len(containers) != 1 or containers[0].get("name") != "ingestion-gateway":
        raise HandledError(
            "Live ingestion-gateway has sidecar or renamed containers.",
            "Inspect live Deployment YAML before migrating.",
        )
    container = containers[0]
    if container.get("env") or container.get("envFrom"):
        raise HandledError(
            "Live ingestion-gateway defines custom env/envFrom (e.g. MESSAGE_BUS=pubsub).",
            "Remove custom env vars before migrating to the Redpanda component gateway.",
        )
    vols = pod_spec.get("volumes") or []
    extra_vols = [v.get("name", "") for v in vols if v.get("name") not in _KNOWN_VOLUME_NAMES]
    if extra_vols:
        raise HandledError(
            f"Live ingestion-gateway mounts unmapped volumes: {extra_vols}.",
            "Only kafka-sasl-credentials and kafka-tls-credentials are supported.",
        )
    return parse_gateway_cli_flags(deployment, strict=True)


def parse_gateway_cli_flags(deployment: dict[str, Any], *, strict: bool = False) -> dict[str, str]:
    """Extracts --key=value, -key value, and bare bool CLI flags from ingestion-gateway."""
    parsed: dict[str, str] = {}
    args = _primary_container(deployment).get("args") or []
    i = 0
    while i < len(args):
        arg, i = args[i], i + 1
        if arg == "--" and not strict:
            break
        if not arg.startswith("-") or arg == "-":
            if strict:
                raise HandledError(f"Unsupported gateway CLI argument '{arg}'.", _FLAG_ERR_HINT)
            continue
        key, sep, val = (arg[2:] if arg.startswith("--") else arg[1:]).partition("=")
        if strict and key not in _KNOWN_GATEWAY_FLAGS:
            raise HandledError(f"Unsupported gateway CLI argument '{arg}'.", _FLAG_ERR_HINT)
        if sep or key in _BOOL_FLAGS:
            parsed[key] = val if sep else "true"
        elif i < len(args) and not args[i].startswith("-"):
            parsed[key], i = args[i], i + 1
        elif strict:
            raise HandledError(f"Unsupported gateway CLI argument '{arg}'.", _FLAG_ERR_HINT)
    return parsed


def _extract_volume_secrets(deployment: dict[str, Any]) -> dict[str, str]:
    """Returns volume_name -> secretName map from live Deployment pod spec."""
    vols = _pod_spec(deployment).get("volumes") or []
    return {
        v["name"]: v["secret"]["secretName"]
        for v in vols
        if v.get("name") and isinstance(v.get("secret"), dict) and v["secret"].get("secretName")
    }


def build_gateway_parameters(
    umbrella_values: dict[str, Any],
    live_dep: dict[str, Any],
    live_hpa: dict[str, Any] | None,
    live_flags: dict[str, str],
) -> dict[str, Any]:
    """Maps live Deployment/HPA state and umbrella values into flat component parameters."""
    ig = umbrella_values.get("ingestionGatewayDeployment") or {}
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
    if live_flags.get("project_id"):
        params["projectId"] = live_flags["project_id"]
    _apply_scaling_and_resources(params, ig, live_dep, live_hpa, container)
    _apply_auth_parameters(params, ig, live_flags, _extract_volume_secrets(live_dep))
    return params


def _apply_scaling_and_resources(
    params: dict[str, Any],
    ig: dict[str, Any],
    live_dep: dict[str, Any],
    live_hpa: dict[str, Any] | None,
    container: dict[str, Any],
) -> None:
    """Populates HPA/replica and per-field CPU/memory resource parameters."""
    hpa_cfg = ig.get("hpa") or {}
    if live_hpa is not None:
        hs = live_hpa.get("spec") or {}
        params["hpaEnabled"] = True
        params["hpaMinReplicas"] = _resolve_int(
            hs.get("minReplicas"), hpa_cfg.get("minReplicas"), default=3, field="hpaMinReplicas"
        )
        params["hpaMaxReplicas"] = _resolve_int(
            hs.get("maxReplicas"), hpa_cfg.get("maxReplicas"), default=10, field="hpaMaxReplicas"
        )
    else:
        params["hpaEnabled"] = False
        dep_rep = (live_dep.get("spec") or {}).get("replicas")
        params["replicas"] = _resolve_int(dep_rep, ig.get("replicas"), default=1, field="replicas")
    c_res, ig_res = container.get("resources") or {}, ig.get("resources") or {}
    c_req, ig_req = c_res.get("requests") or {}, ig_res.get("requests") or {}
    c_lim, ig_lim = c_res.get("limits") or {}, ig_res.get("limits") or {}
    for key, live_d, ig_d, f in (
        ("cpuRequest", c_req, ig_req, "cpu"),
        ("memoryRequest", c_req, ig_req, "memory"),
        ("cpuLimit", c_lim, ig_lim, "cpu"),
        ("memoryLimit", c_lim, ig_lim, "memory"),
    ):
        val = live_d.get(f) or ig_d.get(f)
        if val:
            params[key] = str(val)


def _apply_auth_parameters(
    params: dict[str, Any],
    ig: dict[str, Any],
    live_flags: dict[str, str],
    vol_secrets: dict[str, str],
) -> None:
    """Populates SASL and TLS parameters from live flags/volumes and umbrella values."""
    sasl = ig["sasl"] if isinstance(ig.get("sasl"), dict) else {}
    tls = ig["tls"] if isinstance(ig.get("tls"), dict) else {}
    sasl_raw = live_flags.get("sasl_enabled")
    sasl_on = parse_bool_flag(sasl_raw if sasl_raw is not None else sasl.get("enabled"))
    params["saslEnabled"] = sasl_on
    if sasl_on:
        mech = live_flags.get("sasl_mechanism") or sasl.get("mechanism") or "SCRAM-SHA-256"
        params["saslMechanism"] = str(mech)
        sasl_sec = str(vol_secrets.get("kafka-sasl-credentials") or sasl.get("secretName") or "")
        if sasl_sec:
            params["saslSecretName"] = sasl_sec
    tls_raw = live_flags.get("tls_enabled")
    tls_on = parse_bool_flag(tls_raw if tls_raw is not None else tls.get("enabled"))
    params["tlsEnabled"] = tls_on
    if tls_on:
        tls_sec = str(vol_secrets.get("kafka-tls-credentials") or tls.get("secretName") or "")
        if tls_sec:
            params["tlsSecretName"] = tls_sec
        for pkey, fkey, tkey in (
            ("tlsCaFile", "tls_ca_file", "caFile"),
            ("tlsCertFile", "tls_cert_file", "certFile"),
            ("tlsKeyFile", "tls_key_file", "keyFile"),
        ):
            val = str(live_flags.get(fkey) or tls.get(tkey) or "")
            if val:
                params[pkey] = val


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
    ch = ((umbrella_values.get("metrics") or {}).get("clickhouse")) or {}
    host = str(ch.get("host") or "clickhouse.xmc-metrics.svc.cluster.local")
    port = _resolve_int(ch.get("port"), default=9000, field="metrics.clickhouse.port")
    ep_host = f"[{host}]" if ":" in host and not host.startswith("[") else host
    params: dict[str, Any] = {
        "host": host,
        "clusterEndpoints": f"{ep_host}:{port}",
        "nativePort": port,
        "httpPort": 8123,
        "defaultDatabase": str(ch.get("database") or "default"),
        "user": str(ch.get("user") or "default"),
        "secretName": str(ch.get("secretName") or "clickhouse-secret"),
        "secretPasswordKey": "clickhouse-password",
    }
    if ":" in host:
        params["healthCheckEndpoint"] = f"http://{ep_host}:8123/ping"
    return params


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


def validate_profile_values(
    values_path: Path, namespace: str, profile_dir: Path, repo_root: Path
) -> None:
    """Validates profile-values.yaml via helm template (JSON Schema) and component-linter."""
    with tempfile.TemporaryDirectory(prefix="xmc_profile_lint_") as tmp_dir:
        try:
            cmd = ["helm", "template", "xmc-profile", str(profile_dir), "-n", namespace]
            res_h = run_command(
                [*cmd, "-f", str(values_path)],
                capture_output=True,
                log_on_error=False,
                stream=sys.stderr,
            )
        except (FileNotFoundError, subprocess.CalledProcessError) as e:
            err = (getattr(e, "stderr", "") or str(e)).strip()
            raise HandledError(
                f"Helm template validation failed: {err}",
                "Check generated profile-values.yaml against values.schema.json.",
            ) from e
        (Path(tmp_dir) / "profile.yaml").write_text(res_h.stdout, encoding="utf-8")
        target = LintTarget(manifests_dir=Path(tmp_dir), rules_config=profile_dir / "rules.yaml")
        res = run_linter_on_target(
            target, resolve_linter_binary(None, repo_root), "json", cwd=repo_root
        )
        if not res.success:
            raise HandledError(
                f"component-linter failed: {(res.stderr or res.stdout).strip()}",
                "Fix the reported component-linter errors before migrating.",
            )


def prepare_profile_values(
    release: str,
    namespace: str,
    output_path: Path,
    profile_dir: Path | None = None,
    repo_root: Path | None = None,
) -> dict[str, Any]:
    """Generates and validates profile-values.yaml from live umbrella values and gateway state."""
    validate_dns1123_namespace(namespace)
    try:
        helm_ver = verify_helm_min_version(stream=sys.stderr)
    except (FileNotFoundError, subprocess.CalledProcessError, RuntimeError, ValueError) as e:
        raise HandledError(str(e), "Upgrade Helm CLI to >= 3.14.0 before migrating.") from e
    umbrella_values = fetch_umbrella_values(release, namespace)
    ig = umbrella_values.get("ingestionGatewayDeployment") or {}
    ig_enabled = parse_bool_flag(ig.get("enabled"))
    off = {"components": {n: {"enabled": False, "namespace": namespace} for n in _COMPONENTS}}
    doc = _build_enabled_profile_doc(umbrella_values, namespace) if ig_enabled else off
    rendered = render_profile_yaml(doc)
    try:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(rendered, encoding="utf-8")
    except OSError as e:
        raise HandledError(f"Failed to write '{output_path}': {e}", "Check --output path.") from e
    resolved_root = repo_root or Path(__file__).resolve().parents[3]
    resolved_profile = profile_dir or (resolved_root / "deploy" / "profiles" / "stable-2026a")
    try:
        validate_profile_values(output_path, namespace, resolved_profile, resolved_root)
    except Exception:
        output_path.unlink(missing_ok=True)
        raise
    return {
        "output_path": str(output_path),
        "namespace": namespace,
        "umbrella_release": release,
        "gateway_enabled": ig_enabled,
        "helm_version": helm_ver,
    }


def _build_enabled_profile_doc(umbrella_values: dict[str, Any], namespace: str) -> dict[str, Any]:
    """Audits live cluster objects, verifies secrets, and returns enabled profile dict."""
    live_dep = fetch_live_resource("deployment", GATEWAY_DEPLOYMENT_NAME, namespace, required=True)
    assert isinstance(live_dep, dict)
    live_hpa = fetch_live_resource(
        "horizontalpodautoscaler", GATEWAY_HPA_NAME, namespace, required=False
    )
    gw_params = build_gateway_parameters(
        umbrella_values, live_dep, live_hpa, audit_live_gateway_deployment(live_dep)
    )
    ch_params = build_clickhouse_parameters(umbrella_values)
    if gw_params.get("saslEnabled") and gw_params.get("saslSecretName"):
        verify_secret_exists(str(gw_params["saslSecretName"]), namespace, field_label="SASL")
    if gw_params.get("tlsEnabled") and gw_params.get("tlsSecretName"):
        verify_secret_exists(str(gw_params["tlsSecretName"]), namespace, field_label="TLS")
    secrets_val = umbrella_values.get("secrets")
    csi_val = secrets_val.get("csi") if isinstance(secrets_val, dict) else None
    csi_on = parse_bool_flag(csi_val.get("enabled") if isinstance(csi_val, dict) else None)
    if not csi_on:
        verify_secret_exists(str(ch_params["secretName"]), namespace, field_label="ClickHouse")
    param_map = {
        "redpanda": build_redpanda_parameters(gw_params),
        "clickhouse": ch_params,
        "ingestion-gateway": gw_params,
    }
    return {
        "components": {
            n: {"enabled": True, "namespace": namespace, "parameters": p}
            for n, p in param_map.items()
        }
    }
