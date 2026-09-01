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

"""KinD cluster bootstrap, certificates, secrets, and lifecycle management."""

from __future__ import annotations

import logging
import os
import time

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


class KindClusterManager:
    """Manages ephemeral Kubernetes in Docker (KinD) cluster."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def exists(self) -> bool:
        """Checks if the configured KinD cluster exists."""
        res = run_cmd(
            [self.cfg.kind_bin, "get", "clusters"],
            capture_output=True,
            check=False,
        )
        return self.cfg.cluster_name in res.stdout.splitlines()

    def setup(self) -> None:
        """Creates KinD cluster and applies manifests.

        Raises:
            FileNotFoundError: If GOOGLE_APPLICATION_CREDENTIALS or default ADC file is missing.
        """
        logger.info("=== [KinD] Provisioning cluster %s ===", self.cfg.cluster_name)
        if self.exists():
            logger.info(
                "Cluster %s already exists, deleting first...",
                self.cfg.cluster_name,
            )
            run_cmd(
                [
                    self.cfg.kind_bin,
                    "delete",
                    "cluster",
                    "--name",
                    self.cfg.cluster_name,
                ],
                check=False,
            )

        self._create_cluster()
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        adc_file = self._resolve_adc_file()
        self._create_secrets(adc_file)
        self._generate_tls_secret()
        self._apply_manifests()

    def _create_cluster(self) -> None:
        """Provisions KinD cluster and configures kubectl context."""
        manifests_dir = os.path.join(self.cfg.workspace_root, "test", "e2e", "manifests")
        kind_config = os.path.join(manifests_dir, "kind_config.yaml")
        run_cmd(
            [
                self.cfg.kind_bin,
                "create",
                "cluster",
                "--name",
                self.cfg.cluster_name,
                f"--config={kind_config}",
            ]
        )
        run_cmd(["kubectl", "config", "use-context", f"kind-{self.cfg.cluster_name}"])
        logger.info("Creating namespace 'xmc'...")
        run_cmd(["kubectl", "create", "namespace", "xmc"])

    def _resolve_adc_file(self) -> str:
        """Resolves and validates GCP application default credentials path."""
        adc_file = os.environ.get(
            "GOOGLE_APPLICATION_CREDENTIALS",
            os.path.expanduser("~/.config/gcloud/application_default_credentials.json"),
        )
        if not os.path.isfile(adc_file):
            if "SPANNER_EMULATOR_HOST" in os.environ:
                os.makedirs(self.cfg.temp_dir, exist_ok=True)
                dummy_path = os.path.join(self.cfg.temp_dir, "dummy-credentials.json")
                if not os.path.exists(dummy_path):
                    with open(dummy_path, "w", encoding="utf-8") as f:
                        f.write('{"type": "service_account", "project_id": "dummy"}')
                return dummy_path
            raise FileNotFoundError(
                f"GCP credentials file not found at '{adc_file}'. "
                "A valid credentials file is required to create the 'gcp-sa-key' secret "
                "for Spanner access in the KinD cluster."
            )
        return adc_file

    def _create_secrets(self, adc_file: str) -> None:
        """Creates GCP service account and backend OAuth secrets in Kubernetes."""
        logger.info("Creating GCP service account and backend secrets...")
        run_cmd(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "gcp-sa-key",
                "-n",
                "xmc",
                f"--from-file=key.json={adc_file}",
            ]
        )
        run_cmd(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "backend-oauth-secrets",
                "-n",
                "xmc",
                "--from-literal=jwt-secret=e2e-development-secret-must-be-at-least-32-chars-long",
                "--from-literal=client-secret=xmc-client-secret",
                f"--from-file=key.json={adc_file}",
            ]
        )

    def _generate_tls_secret(self) -> None:
        """Generates self-signed TLS certificates and creates validation-secret."""
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        tls_key_path = os.path.join(self.cfg.temp_dir, "e2e-validation.key")
        tls_crt_path = os.path.join(self.cfg.temp_dir, "e2e-validation.crt")
        if not os.path.exists(tls_key_path):
            run_cmd(
                [
                    "openssl",
                    "genpkey",
                    "-algorithm",
                    "RSA",
                    "-out",
                    tls_key_path,
                    "-pkeyopt",
                    "rsa_keygen_bits:2048",
                ]
            )
            run_cmd(
                [
                    "openssl",
                    "req",
                    "-new",
                    "-x509",
                    "-key",
                    tls_key_path,
                    "-out",
                    tls_crt_path,
                    "-days",
                    "365",
                    "-subj",
                    "/CN=xmc-e2e-signer",
                ]
            )
        run_cmd(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "validation-secret",
                "-n",
                "xmc",
                f"--from-file=tls.key={tls_key_path}",
                f"--from-file=tls.crt={tls_crt_path}",
            ]
        )

    def _apply_manifests(self) -> None:
        """Applies dummy CRDs, Redis, Dex OIDC, and Gateway Proxy manifests."""
        logger.info("Applying dummy CRDs, Redis, Dex, and Gateway Proxy...")
        manifests_dir = os.path.join(self.cfg.workspace_root, "test", "e2e", "manifests")
        manifests = ["dummy_crds.yaml", "redis.yaml", "dex.yaml", "gateway_proxy.yaml"]
        cmd = ["kubectl", "apply"]
        for manifest in manifests:
            cmd.extend(["-f", os.path.join(manifests_dir, manifest)])
        run_cmd(cmd)

    def load_images(self, images: list[str]) -> None:
        """Loads Docker images into KinD nodes with retry."""
        if not images:
            return
        logger.info("=== [KinD] Loading container images into cluster ===")
        for img in images:
            logger.info("[KinD] Loading image %s...", img)
            for attempt in range(1, 4):
                try:
                    run_cmd(
                        [
                            self.cfg.kind_bin,
                            "load",
                            "docker-image",
                            img,
                            "--name",
                            self.cfg.cluster_name,
                        ]
                    )
                    break
                except Exception as e:
                    if attempt == 3:
                        raise
                    logger.warning(
                        "[KinD] Image %s load failed (attempt %d/3): %s. Retrying in 2s...",
                        img,
                        attempt,
                        e,
                    )
                    time.sleep(2)

    def teardown(self) -> None:
        """Destroys the KinD cluster."""
        if self.cfg.keep_cluster:
            return
        try:
            if self.exists():
                logger.info(
                    "=== [KinD] Deleting cluster %s ===",
                    self.cfg.cluster_name,
                )
                run_cmd(
                    [
                        self.cfg.kind_bin,
                        "delete",
                        "cluster",
                        "--name",
                        self.cfg.cluster_name,
                    ],
                    check=False,
                )
        except Exception as e:
            logger.warning("Failed to safely teardown KinD cluster: %s", e)
        finally:
            import shutil

            shutil.rmtree(self.cfg.temp_dir, ignore_errors=True)
