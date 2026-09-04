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

import logging
from pathlib import Path

from tools.common.tool_utils import log_info, log_success
from tools.dev_env.config import LocalDevConfig
from tools.dev_env.helm_deployer import HelmDeployer
from tools.dev_env.image_builder import ImageBuilder
from tools.dev_env.kind_manager import KindManager, check_preflight_tools
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
