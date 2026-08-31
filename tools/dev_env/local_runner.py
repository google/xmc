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

"""Local KinD development environment lifecycle orchestrator."""

import concurrent.futures
import logging
import time
from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig
from tools.dev_env.helm_deployer import HelmDeployer
from tools.dev_env.image_builder import ImageBuilder
from tools.dev_env.kind_manager import KindManager, check_preflight_tools
from tools.dev_env.spanner_manager import SpannerManager

_LOGGER = logging.getLogger("xmc.dev_env.local_runner")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

SERVICE_ALIASES: dict[str, str] = {
    "ess": "experiment_state_server",
    "xid": "xid_service",
    "rod": "rod_data_provider",
    "gw": "ingestion_gateway",
    "gateway": "ingestion_gateway",
    "frontend": "ui",
    "web": "ui",
    "dash": "dashboard_service",
    "dashboard": "dashboard_service",
    "health": "health_service",
    "comp": "components",
}


def resolve_service_name(alias_or_name: str) -> str:
    """Resolves service aliases to canonical snake_case service names."""
    normalized = alias_or_name.strip().lower().replace("-", "_")
    return SERVICE_ALIASES.get(normalized, normalized)


def resolve_service_names(names: list[str]) -> list[str]:
    """Resolves a list of service names or aliases to canonical service names."""
    return list(dict.fromkeys(resolve_service_name(n) for n in names if n and n.strip()))


def service_to_k8s_deployment_name(service: str) -> str:
    """Converts a service name or alias to Kubernetes Deployment name.

    Converts underscores to dashes and adds '-deployment' suffix, with exceptions
    for special gateway components.

    Examples:
        'dashboard_service' -> 'dashboard-service-deployment'
        'experiment_state_server' -> 'experiment-state-server-deployment'
        'ingestion_gateway' -> 'gateway-proxy'
        'ui' -> 'ui-deployment'
    """
    canonical = resolve_service_name(service)
    svc_lower = canonical.lower().replace("_", "-")
    if svc_lower in ("ingestion-gateway", "gateway-proxy"):
        return "gateway-proxy"
    if svc_lower.endswith("-deployment"):
        return svc_lower
    return f"{svc_lower}-deployment"


class LocalRunner:
    """Orchestrates local development lifecycle against Kubernetes in Docker (KinD)."""

    def __init__(self, config: LocalDevConfig, repo_root: Path | None = None) -> None:
        """Initializes LocalRunner with configuration settings."""
        self.config, self.repo_root = config, repo_root or _REPO_ROOT
        self.spanner_mgr = SpannerManager(config, repo_root=self.repo_root)
        self.kind_mgr = KindManager(config, repo_root=self.repo_root)
        self.image_builder = ImageBuilder(config, repo_root=self.repo_root)
        self.helm_deployer = HelmDeployer(config, repo_root=self.repo_root)

    def up(
        self,
        recreate: bool = False,
        skip_build: bool = False,
        skip_helm: bool = False,
    ) -> None:
        """Provisions the complete local KinD environment end-to-end.

        Executes the bootstrap pipeline:
        1. Pre-flight host binary check (Docker, KinD, kubectl, Helm, OpenSSL, gcloud, Bazel).
        2. Cloud Spanner instance and database provisioning with schema migrations.
        3. KinD cluster creation with ingress port mappings (8080 UI, 5556 Dex).
        4. GCP ADC credential, JWT OAuth, and self-signed TLS secret injection.
        5. Support manifest application (dummy CRDs, Redis, Dex OIDC, Gateway Proxy).
        6. Parallel OCI image compilation via Bazel and loading into KinD nodes.
        7. Dynamic values generation and sanitized Helm chart deployment.
        8. Displays formatted completion banner with active endpoints.

        Args:
            recreate: If True, destroys any pre-existing KinD cluster with the same
                name before recreating it.
            skip_build: If True, skips Bazel image building and assumes images exist.
            skip_helm: If True, provisions cluster and images but skips Helm deployment.
        """
        _LOGGER.debug(
            "Starting local environment bootstrap (recreate=%s, skip_build=%s, skip_helm=%s)",
            recreate,
            skip_build,
            skip_helm,
        )
        log_info("=== [XMC Local Dev] Starting environment bootstrap ===")
        check_preflight_tools()
        self.spanner_mgr.setup()
        self.kind_mgr.create_cluster(recreate=recreate)
        self.kind_mgr.ensure_secrets()
        self.kind_mgr.apply_manifests()
        images = self.image_builder.build_images(skip_build=skip_build)
        if not skip_build:
            self.image_builder.load_images_into_kind(images)
        if not skip_helm:
            self.helm_deployer.deploy()
        else:
            log_info("Skipping Helm deployment (--no-deploy-helm specified).")
        self._display_ready_banner()

    def down(self, drop_db: bool = False) -> None:
        """Tears down the local KinD cluster and cleans up local development artifacts.

        Deletes the KinD cluster and its temporary credential directory. Optionally
        drops the ephemeral Cloud Spanner databases (ESS and XID).

        Args:
            drop_db: If True, drops the Cloud Spanner development databases.
                Defaults to False to preserve test database records across restarts.
        """
        _LOGGER.debug("Tearing down local KinD environment (drop_db=%s)", drop_db)
        log_info("=== [XMC Local Dev] Tearing down environment ===")
        self.kind_mgr.delete_cluster()
        if drop_db:
            self.spanner_mgr.drop_databases()
        else:
            log_info(
                f"Preserving Spanner databases ('{self.config.ess_database}', "
                f"'{self.config.xid_database}'). Use --drop-db to delete them."
            )
        log_success("Local KinD development environment teardown complete.")

    def _resolve_target_services(
        self,
        services: list[str] | None = None,
        helm: bool = False,
    ) -> tuple[set[str], bool]:
        """Resolves target services and Helm requirement from explicit arguments."""
        target_services: set[str] = set()
        helm_needed = helm
        raw_services = services or []
        lowercase_services = {s.strip().lower() for s in raw_services if s and s.strip()}

        if not raw_services and not helm:
            raise ValueError(
                "No services specified to reload. Specify microservice names (e.g. 'ess', 'ui', 'all') or use --helm."
            )

        if any(
            s.strip().lower() in ("helm", "chart", "charts")
            for s in raw_services
            if s and s.strip()
        ):
            helm_needed = True

        explicit_services = [
            s.strip()
            for s in raw_services
            if s and s.strip() and s.strip().lower() not in ("all", "helm", "chart", "charts")
        ]
        if explicit_services:
            resolved = resolve_service_names(explicit_services)
            invalid_or_disabled = [s for s in resolved if s not in self.config.enabled_services]
            if invalid_or_disabled:
                raise ValueError(
                    "The following services are invalid or not enabled in the current"
                    f" configuration: {', '.join(invalid_or_disabled)}. Enabled services:"
                    f" {', '.join(self.config.enabled_services)}"
                )
            target_services.update(resolved)

        if "all" in lowercase_services:
            target_services.update(self.config.enabled_services)

        return target_services, helm_needed

    def _rebuild_and_load_services(self, sorted_targets: list[str]) -> None:
        """Compiles image tarballs and imports them into the KinD cluster."""
        log_info(f"=== [XMC Local Dev] Hot-reloading: {', '.join(sorted_targets)} ===")
        images = self.image_builder.build_images(services=sorted_targets)
        self.image_builder.load_images_into_kind(images)

    def _restart_deployments(
        self, ctx: str, sorted_targets: list[str]
    ) -> tuple[list[str], list[str]]:
        """Triggers rollout restart across affected deployments."""
        triggered: list[str] = []
        failed: list[str] = []
        for svc in sorted_targets:
            deploy = service_to_k8s_deployment_name(svc)
            log_info(f"Restarting deployment '{deploy}' in namespace 'xmc'...")
            try:
                run_command(
                    [
                        "kubectl",
                        "--context",
                        ctx,
                        "rollout",
                        "restart",
                        f"deployment/{deploy}",
                        "-n",
                        "xmc",
                    ],
                    check=True,
                )
                triggered.append(deploy)
            except Exception as e:
                log_info(f"Failed to restart deployment '{deploy}': {e}. Skipping.")
                failed.append(deploy)
        return triggered, failed

    def _wait_for_restarted_rollouts(self, ctx: str, deployments: list[str]) -> list[str]:
        """Monitors rollouts in parallel and returns list of failed deployment names."""

        def _wait_single(deploy_name: str) -> str | None:
            try:
                run_command(
                    [
                        "kubectl",
                        "--context",
                        ctx,
                        "rollout",
                        "status",
                        f"deployment/{deploy_name}",
                        "-n",
                        "xmc",
                        "--timeout=120s",
                    ],
                    check=True,
                )
                return None
            except Exception as err:
                log_info(f"Failed to wait for deployment '{deploy_name}' rollout: {err}. Skipping.")
                return deploy_name

        if not deployments:
            return []
        with concurrent.futures.ThreadPoolExecutor(
            max_workers=min(len(deployments), 8)
        ) as executor:
            results = executor.map(_wait_single, deployments)
            return [r for r in results if r is not None]

    def reload(self, services: list[str] | None = None, helm: bool = False) -> bool:
        """Recompiles and hot-reloads selected microservices into the running cluster.

        Fast inner-loop reload pipeline:
        1. Checks that the KinD development cluster is active.
        2. Validates and merges explicitly specified services or 'all' keyword.
        3. Recompiles OCI container images in parallel with Bazel.
        4. Imports new image layers into KinD cluster nodes via `kind load docker-image`.
        5. Upgrades Helm release if `--helm` was requested.
        6. Triggers non-blocking `kubectl rollout restart` across affected deployments.
        7. Concurrently monitors rollout completion across worker threads with timeout bounds.
        8. Reports sub-second execution elapsed time and success status.

        Args:
            services: List of microservice names or aliases to reload (e.g. 'ess', 'ui', 'all').
            helm: If True, forces a Helm chart upgrade.

        Returns:
            True if any service or Helm upgrade was performed.

        Raises:
            RuntimeError: If the KinD cluster is not currently running or any rollouts fail.
            ValueError: If services list is empty or any requested service is invalid.
        """
        if not self.kind_mgr.exists():
            raise RuntimeError(
                f"KinD cluster '{self.config.cluster_name}' is not running. "
                "Run 'python3 tools/dev_env/dev_env.py up' first."
            )

        target_services, helm_needed = self._resolve_target_services(services, helm=helm)
        start_time = time.perf_counter()
        sorted_targets = sorted(target_services)

        if sorted_targets:
            self._rebuild_and_load_services(sorted_targets)

        if helm_needed:
            log_info("=== [Helm] Updating Helm release on KinD cluster ===")
            self.helm_deployer.deploy()
            self.kind_mgr.apply_manifests()

        if sorted_targets:
            ctx = self.kind_mgr.kube_context
            triggered, failed = self._restart_deployments(ctx, sorted_targets)
            rollout_failed = self._wait_for_restarted_rollouts(ctx, triggered)
            failed.extend(rollout_failed)
            elapsed = time.perf_counter() - start_time
            msg_suffix = " and Helm release" if helm_needed else ""
            if failed:
                raise RuntimeError(
                    f"Services{msg_suffix} reloaded in {elapsed:.2f}s with {len(failed)} failure(s): "
                    f"{', '.join(sorted_targets)} (failed: {', '.join(failed)})"
                )
            log_success(
                f"Services{msg_suffix} reloaded in {elapsed:.2f}s: {', '.join(sorted_targets)}"
            )
            return True

        if helm_needed:
            elapsed = time.perf_counter() - start_time
            log_success(f"Helm release reloaded in {elapsed:.2f}s")
            return True

        return False

    def _display_ready_banner(self) -> None:
        """Displays formatted completion summary with clickable URLs."""
        lines = [
            "=" * 80,
            "🚀 XMC Local KinD Development Environment is Ready!",
            "=" * 80,
            "• Web UI & API Gateway:   http://localhost:8080",
            "• Dex OIDC Provider:      http://localhost:5556/dex",
            "• Default Admin Account:  admin@xmc.local (Password: password123)",
            "• Default Viewer Account: viewer@xmc.local (Password: password123)",
            f"• Cloud Spanner Instance: {self.config.spanner_instance}",
            f"• ESS Database:           {self.config.ess_database}",
            f"• XID Database:           {self.config.xid_database}",
            f"• Kubernetes Context:     {self.kind_mgr.kube_context}",
            "=" * 80,
        ]
        log_info("\n" + "\n".join(lines) + "\n")
