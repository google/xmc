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

"""Helm chart values generation, release orchestration, and deployment for local dev."""

from __future__ import annotations

import getpass
import json
import logging
import os
from pathlib import Path
from typing import Any

from tools.common.tool_utils import log_error, log_info, log_success, run_command
from tools.dev_env.chart_sanitizer import ChartSanitizer
from tools.dev_env.cluster_diagnostics import ClusterDiagnostics
from tools.dev_env.config import LocalDevConfig

_LOGGER = logging.getLogger("xmc.dev_env.helm_deployer")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

try:
    import yaml

    _HAS_YAML = True
except ImportError:
    _HAS_YAML = False


def _dump_yaml(data: dict[str, Any]) -> str:
    """Serializes a dictionary to YAML format using PyYAML or a clean JSON fallback."""
    if _HAS_YAML:
        return yaml.safe_dump(data, default_flow_style=False, sort_keys=False)
    return json.dumps(data, indent=2)


def service_to_deployment_key(service: str) -> str:
    """Converts a snake_case service name to camelCase Helm deployment key.

    Examples:
        'dashboard_service' -> 'dashboardServiceDeployment'
        'health_service' -> 'healthServiceDeployment'
        'ingestion_gateway' -> 'ingestionGatewayDeployment'
        'components' -> 'componentsDeployment'
        'rod_data_provider' -> 'rodDeployment'
        'experiment_state_server' -> 'essDeployment'
    """
    if service in ("experiment_state_server", "ess"):
        return "essDeployment"
    if service in ("rod_data_provider", "rod"):
        return "rodDeployment"

    parts = service.split("_")
    camel = parts[0].lower() + "".join(p.capitalize() for p in parts[1:])
    return f"{camel}Deployment"


def service_to_image_key(service: str) -> str:
    """Derives Helm custom image value key from service name.

    Examples:
        'dashboard_service' -> 'dashboard_service_custom_image'
        'experiment_state_server' -> 'ess_custom_image'
        'rod_data_provider' -> 'rod_custom_image'
    """
    if service in ("experiment_state_server", "ess"):
        return "ess_custom_image"
    if service in ("rod_data_provider", "rod"):
        return "rod_custom_image"
    return f"{service.lower()}_custom_image"


class HelmDeployer:
    """Manages Helm release values generation, deployment orchestration, and rollout verification."""

    def __init__(
        self,
        config: LocalDevConfig,
        repo_root: Path | None = None,
        temp_dir: Path | None = None,
        sanitizer: ChartSanitizer | None = None,
        diagnostics: ClusterDiagnostics | None = None,
    ) -> None:
        """Initializes HelmDeployer with development configuration."""
        self.config, self.repo_root = config, repo_root or _REPO_ROOT
        try:
            username = getpass.getuser()
        except (KeyError, OSError, ImportError):
            getuid_fn = getattr(os, "getuid", None)
            try:
                uid_str = str(getuid_fn()) if callable(getuid_fn) else "unknown"
            except Exception:
                uid_str = "unknown"
            username = os.environ.get("USER") or os.environ.get("LOGNAME") or uid_str
        self.temp_dir = (
            temp_dir or self.repo_root / ".xmc_dev" / f"{username}_{config.cluster_name}"
        )
        self.helm_dir = self.temp_dir / "helm"
        self.values_file = self.temp_dir / "values.yaml"
        self.sanitizer = sanitizer or ChartSanitizer(
            source_chart=self.repo_root / "deploy" / "helm",
            target_dir=self.helm_dir,
        )
        self.diagnostics = diagnostics or ClusterDiagnostics(
            kube_context=self.kube_context,
            output_dir=self.temp_dir,
        )

    @property
    def kube_context(self) -> str:
        """Returns the kubectl context name for this KinD cluster."""
        return f"kind-{self.config.cluster_name}"

    def prepare_local_chart(self) -> Path:
        """Copies and sanitizes Helm chart templates for local development."""
        return self.sanitizer.prepare_sanitized_chart()

    def capture_diagnostics(self, namespace: str = "xmc") -> Path:
        """Captures diagnostic telemetry across namespaces on failure."""
        return self.diagnostics.capture_diagnostics(namespace=namespace)

    def _build_values_dict(self, cfg: LocalDevConfig) -> dict[str, Any]:
        """Constructs the dictionary of Helm values for local deployment."""
        values: dict[str, Any] = {
            "projectId": cfg.project_id,
            "environment": cfg.environment,
            "clusterName": cfg.cluster_name,
            "gcpServiceAccount": f"xmc-dev-sa@{cfg.project_id}.iam.gserviceaccount.com",
            "experimentStateServer": {
                "spannerDatabaseName": (
                    f"projects/{cfg.project_id}/instances/{cfg.spanner_instance}/"
                    f"databases/{cfg.ess_database}"
                )
            },
            "xidService": {
                "spannerDatabaseName": (
                    f"projects/{cfg.project_id}/instances/{cfg.spanner_instance}/"
                    f"databases/{cfg.xid_database}"
                )
            },
        }

        for s in cfg.enabled_services:
            dep_key = service_to_deployment_key(s)
            img_key = service_to_image_key(s)
            values[dep_key] = {"enabled": True}
            values[img_key] = f"{s.lower()}_image:latest"

        return values

    def generate_values_file(
        self,
        config: LocalDevConfig | None = None,
        output_path: Path | None = None,
    ) -> Path:
        """Generates dynamic Helm values connecting to local Dex, Redis, and Spanner.

        Interpolates configuration settings into the local development values dictionary,
        wiring endpoints for local in-cluster services (Dex OIDC issuer, Redis address,
        and Cloud Spanner database paths).

        Args:
            config: Optional LocalDevConfig override; defaults to self.config.
            output_path: Optional destination Path for values.yaml; defaults to self.values_file.

        Returns:
            Absolute Path to the written Helm values YAML file.
        """
        cfg = config or self.config
        target_path = output_path or self.values_file
        target_path.parent.mkdir(parents=True, exist_ok=True)

        values = self._build_values_dict(cfg)
        content = _dump_yaml(values) + "\n"
        target_path.write_text(content, encoding="utf-8")
        _LOGGER.debug("Wrote Helm values (%d bytes) to %s", len(content), target_path)
        log_info(f"Generated Helm values file: '{target_path}'")
        return target_path

    def _check_dex_rollout(self, ctx: str, timeout_flag: str) -> None:
        """Checks rollout status for Dex in the 'dex' namespace if present."""
        check_dex_cmd = ["kubectl", "--context", ctx, "get", "deployment/dex", "-n", "dex"]
        if (
            run_command(check_dex_cmd, check=False, capture_output=True, log_cmd=False).returncode
            == 0
        ):
            _LOGGER.debug("Checking rollout status for deployment/dex in namespace dex")
            run_command(
                [
                    "kubectl",
                    "--context",
                    ctx,
                    "rollout",
                    "status",
                    "deployment/dex",
                    "-n",
                    "dex",
                    timeout_flag,
                ]
            )
        else:
            _LOGGER.debug("Deployment dex not found in namespace dex, skipping rollout check")

    def _discover_namespace_deployments(self, ctx: str, namespace: str) -> list[str]:
        """Discovers all deployment names in the specified namespace."""
        get_deps_cmd = [
            "kubectl",
            "--context",
            ctx,
            "get",
            "deployments",
            "-n",
            namespace,
            "-o",
            "jsonpath={.items[*].metadata.name}",
        ]
        res = run_command(get_deps_cmd, check=False, capture_output=True, log_cmd=False)
        if res.returncode != 0:
            stderr_str = (
                res.stderr.decode("utf-8", errors="replace")
                if isinstance(res.stderr, bytes)
                else str(res.stderr or "")
            )
            raise RuntimeError(
                f"Failed to discover deployments in namespace {namespace} "
                f"(exit code {res.returncode}): {stderr_str}"
            )

        stdout = (
            res.stdout.decode("utf-8", errors="replace")
            if isinstance(res.stdout, bytes)
            else (res.stdout or "")
        )
        return sorted(stdout.split()) if stdout.strip() else []

    def wait_for_rollouts(self, namespace: str = "xmc", timeout_seconds: int = 180) -> None:
        """Waits for core deployments across namespaces to complete rollouts.

        Monitors rollout status sequentially:
        1. Dex deployment in the 'dex' namespace.
        2. Core XMC workloads in the target namespace.

        If any deployment rollout fails or times out, automatically captures
        cluster diagnostics before propagating the error.

        Args:
            namespace: Target Kubernetes namespace containing XMC deployments (default: 'xmc').
            timeout_seconds: Maximum seconds to wait for each individual deployment rollout.
        """
        log_info(f"Waiting for rollout in namespace '{namespace}' ({timeout_seconds}s)...")
        ctx, timeout_flag = self.kube_context, f"--timeout={timeout_seconds}s"
        try:
            self._check_dex_rollout(ctx, timeout_flag)
            deployments = self._discover_namespace_deployments(ctx, namespace)
            if not deployments:
                log_info(f"No deployments found in namespace '{namespace}' to monitor.")
                return
            for dep in deployments:
                _LOGGER.debug(
                    "Checking rollout status for deployment/%s in namespace %s", dep, namespace
                )
                run_command(
                    [
                        "kubectl",
                        "--context",
                        ctx,
                        "rollout",
                        "status",
                        f"deployment/{dep}",
                        "-n",
                        namespace,
                        timeout_flag,
                    ]
                )
            log_success("All core deployments rolled out successfully.")
        except Exception:
            try:
                self.capture_diagnostics(namespace=namespace)
            except Exception as e:
                log_error(f"Failed to capture diagnostics: {e}")
            raise

    def deploy(
        self,
        chart_path: Path | str | None = None,
        values_path: Path | str | None = None,
        namespace: str = "xmc",
        timeout: int = 180,
    ) -> None:
        """Executes sanitized Helm release upgrade/install and waits for rollout confirmation.

        Sanitizes chart templates for local KinD execution, runs `helm upgrade --install`,
        restarts gateway-proxy, and verifies deployment rollouts.

        Args:
            chart_path: Optional Path to the Helm chart directory; sanitized if omitted.
            values_path: Optional Path to custom values YAML file; generated if omitted.
            namespace: Target Kubernetes namespace (default: 'xmc').
            timeout: Maximum rollout wait timeout in seconds (default: 180).

        Raises:
            FileNotFoundError: If the specified Helm chart directory does not exist.
        """
        chart_dir = Path(chart_path) if chart_path else self.prepare_local_chart()
        try:
            is_dir = chart_dir.is_dir()
        except OSError:
            is_dir = False
        if not is_dir:
            raise FileNotFoundError(f"Helm chart directory not found at '{chart_dir}'")
        if values_path:
            val_file = Path(values_path)
            try:
                is_file = val_file.is_file()
            except OSError:
                is_file = False
            if not is_file:
                raise FileNotFoundError(f"Custom values file not found at '{val_file}'")
        else:
            val_file = self.generate_values_file()
        ctx = self.kube_context
        log_info(f"Deploying XMC Helm release to '{self.config.cluster_name}'...")
        helm_cmd = [
            "helm",
            "upgrade",
            "--install",
            "xmc",
            str(chart_dir),
            "--kube-context",
            ctx,
            "--create-namespace",
        ]
        local_values_file = chart_dir / "values-local.yaml"
        if local_values_file.is_file():
            helm_cmd.extend(["-f", str(local_values_file)])
        helm_cmd.extend(["-n", namespace, "-f", str(val_file)])
        run_command(helm_cmd)
        gw_cmd = ["kubectl", "--context", ctx, "rollout", "restart", "deployment/gateway-proxy"]
        run_command(gw_cmd + ["-n", namespace], check=False)
        self.wait_for_rollouts(namespace=namespace, timeout_seconds=timeout)
        log_success("XMC Stack successfully deployed via Helm.")
