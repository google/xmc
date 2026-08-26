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

from tools.common.tool_utils import log_error, log_info, log_success, run_command
from tools.dev_env.change_detector import ChangeDetector
from tools.dev_env.config import LocalDevConfig
from tools.dev_env.helm_deployer import HelmDeployer
from tools.dev_env.image_builder import ImageBuilder
from tools.dev_env.kind_manager import KindManager, check_preflight_tools
from tools.dev_env.service_resolver import (
    resolve_target_services,
    service_to_k8s_deployment_name,
)
from tools.dev_env.spanner_manager import SpannerManager

_LOGGER = logging.getLogger("xmc.dev_env.local_runner")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent


class LocalRunner:
    """Orchestrates local development lifecycle against Kubernetes in Docker (KinD)."""

    def __init__(self, config: LocalDevConfig, repo_root: Path | None = None) -> None:
        """Initializes LocalRunner with configuration settings."""
        self.config, self.repo_root = config, repo_root or _REPO_ROOT
        self.spanner_mgr = SpannerManager(config, repo_root=self.repo_root)
        self.kind_mgr = KindManager(config, repo_root=self.repo_root)
        self.image_builder = ImageBuilder(config, repo_root=self.repo_root)
        self.helm_deployer = HelmDeployer(config, repo_root=self.repo_root)
        self.change_detector = ChangeDetector(
            repo_root=self.repo_root,
            enabled_services=self.config.enabled_services,
        )

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

    def reload(
        self,
        services: list[str] | None = None,
        watch: bool = False,
        helm: bool = False,
        interval: float = 1.5,
    ) -> bool | None:
        """Recompiles and hot-reloads selected or modified microservices into the running cluster.

        Can execute either in single-shot mode (default) or continuous watch mode (`watch=True`),
        polling the filesystem for file saves and triggering debounced reloads.

        Args:
            services: Optional list of microservice names or aliases to rebuild and restart.
                Supports special keywords 'auto' (detect from git) and 'all' (all enabled services).
            watch: If True, continuously watches the repository workspace for file changes.
            helm: If True, explicitly triggers a Helm chart upgrade and manifest sync.
            interval: Polling interval in seconds when operating in continuous watch mode.

        Raises:
            RuntimeError: If the KinD cluster is not currently running.
            ValueError: If `interval <= 0` in watch mode or if any requested service is invalid.
        """
        if not self.kind_mgr.exists():
            raise RuntimeError(
                f"KinD cluster '{self.config.cluster_name}' is not running. "
                "Run 'python3 tools/dev_env/dev_env.py up' first."
            )

        if watch:
            if interval <= 0:
                raise ValueError("Watch interval must be greater than 0 seconds.")
            self._watch_loop(services=services, interval=interval, helm=helm)
            return

        self._reload_once(services=services, helm=helm)

    def _get_workspace_signature(self) -> dict[str, tuple[int, int]]:
        """Computes a lightweight signature (mtime + file size) of modified git files.

        Returns:
            Dictionary mapping relative file paths to (modification_time_ns, size_bytes).
        """
        files = self.change_detector.get_modified_files()
        sig: dict[str, tuple[int, int]] = {}
        for f in files:
            try:
                st = (self.repo_root / f).stat()
                sig[f] = (st.st_mtime_ns, st.st_size)
            except OSError:
                sig[f] = (0, 0)
        return sig

    def _watch_loop(
        self,
        services: list[str] | None = None,
        interval: float = 1.5,
        helm: bool = False,
    ) -> None:
        """Continuously polls workspace for file modifications and triggers hot-reloads.

        Executes an initial reload on start, then loops with `time.sleep(interval)`.
        When a filesystem change is observed, debounces rapid save events with a short
        500ms sleep before invoking `_reload_once`. Handles KeyboardInterrupt gracefully.

        Args:
            services: Optional list of service names or aliases to scope reloads to.
            interval: Polling interval in seconds between workspace signature checks.
            helm: If True, triggers Helm upgrade on initial reload.
        """
        log_info("👀 [Watch Mode] Watching workspace for file changes (press Ctrl+C to stop)...")
        try:
            self._reload_once(services=services, helm=helm)
        except Exception as e:
            log_error(f"Initial reload failed: {e}. Watching for next save...")

        last_sig = self._get_workspace_signature()
        try:
            while True:
                time.sleep(interval)
                current_sig = self._get_workspace_signature()
                if current_sig and current_sig != last_sig:
                    time.sleep(0.5)  # debounce rapid filesystem events
                    current_sig = self._get_workspace_signature()
                    if not current_sig:
                        last_sig = {}
                        continue
                    delta_files = [f for f, sig in current_sig.items() if sig != last_sig.get(f)]
                    deleted_files = [f for f in last_sig if f not in current_sig]
                    changed_delta = sorted(set(delta_files + deleted_files))
                    try:
                        self._reload_once(services=services, changed_files=changed_delta)
                    except Exception as e:
                        log_error(f"Reload failed: {e}. Watching for next save...")
                    last_sig = current_sig
                elif not current_sig and last_sig:
                    last_sig = {}
        except KeyboardInterrupt:
            log_info("\n🛑 Stopped watching workspace.")

    def _resolve_target_services(
        self,
        services: list[str] | None = None,
        changed_files: list[str] | None = None,
        helm: bool = False,
    ) -> tuple[set[str], bool]:
        """Resolves target services and Helm requirement from args and git status."""
        target_services: set[str] = set()
        helm_needed = helm
        raw_services = services or []
        lowercase_services = {s.strip().lower() for s in raw_services if s and s.strip()}

        if ("auto" in lowercase_services) or (not raw_services and not helm):
            log_info("=== [ChangeDetector] Auto-detecting modified services from git ===")
            det = self.change_detector.detect_changes(files=changed_files)
            target_services.update(det.services)
            if det.helm_upgrade_needed:
                helm_needed = True
            for svc, r in det.reasons.items():
                files_str = ", ".join(r[:2]) + ("..." if len(r) > 2 else "")
                log_info(f"  • {svc}: triggered by {len(r)} file(s) ({files_str})")

        non_auto_services = [
            s for s in raw_services if s and s.strip() and s.strip().lower() != "auto"
        ]
        if non_auto_services:
            explicit_targets, explicit_helm = resolve_target_services(
                services=non_auto_services,
                enabled_services=self.change_detector.enabled_services,
                helm=helm_needed,
            )
            if changed_files is not None:
                det = self.change_detector.detect_changes(files=changed_files)
                if det.helm_upgrade_needed:
                    helm_needed = True
                target_services.update(s for s in explicit_targets if s in det.services)
            else:
                target_services.update(explicit_targets)
                helm_needed = explicit_helm

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
                    capture_output=True,
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

    def _reload_once(
        self,
        services: list[str] | None = None,
        changed_files: list[str] | None = None,
        helm: bool = False,
    ) -> bool:
        """Executes a single hot-reload iteration.

        Compiles modified microservices via Bazel, loads OCI images into KinD,
        updates Helm release if manifests or chart changed, and triggers concurrent
        Kubernetes deployment restarts.

        Args:
            services: Optional explicit list of services to reload.
            changed_files: Optional list of specific modified files triggering this reload.
            helm: If True, forces a Helm chart upgrade.

        Returns:
            True if any service or Helm upgrade was performed; False if workspace was clean.

        Raises:
            RuntimeError: If the KinD cluster is not currently running or any rollouts fail.
            ValueError: If any explicitly requested service is unrecognized or disabled.
        """
        target_services, helm_needed = self._resolve_target_services(
            services=services, changed_files=changed_files, helm=helm
        )
        if not target_services and not helm_needed:
            log_info("No modified microservices or Helm manifests detected. Working tree clean.")
            return False
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
