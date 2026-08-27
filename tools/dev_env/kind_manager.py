#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""KinD (Kubernetes in Docker) Cluster Manager for Local Development.

Provides automated provisioning, host pre-flight verification, ADC credential
injection, self-signed TLS certificate generation, and lifecycle management for
local XMC KinD clusters.
"""

import getpass
import logging
import os
import shutil
import tempfile
from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig

_LOGGER = logging.getLogger("xmc.dev_env.kind")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

REQUIRED_PREFLIGHT_TOOLS: list[str] = [
    "docker",
    "kind",
    "kubectl",
    "helm",
    "openssl",
    "gcloud",
    "bazel",
]


def check_preflight_tools(required_tools: list[str] | None = None) -> None:
    """Verifies that all required host binaries and CLI tools are installed."""
    tools = required_tools or REQUIRED_PREFLIGHT_TOOLS
    missing = [tool for tool in tools if shutil.which(tool) is None]
    if missing:
        raise RuntimeError(
            f"Missing required pre-flight tool(s): {', '.join(missing)}. "
            "Please ensure Docker, KinD, kubectl, Helm, OpenSSL, gcloud, and Bazel "
            "are installed and available in your PATH."
        )
    _LOGGER.debug("Pre-flight check passed: %s", tools)
    log_success("Pre-flight check passed: all required developer tools are installed.")


class KindManager:
    """Manages local Kubernetes in Docker (KinD) cluster lifecycle and secrets."""

    def __init__(
        self,
        config: LocalDevConfig,
        repo_root: Path | None = None,
        temp_dir: Path | None = None,
    ) -> None:
        """Initializes KindManager with configuration and directory paths."""
        self.config = config
        self.repo_root = repo_root or _REPO_ROOT
        self.temp_dir = (
            temp_dir
            or Path(tempfile.gettempdir()) / f"xmc_dev_{getpass.getuser()}_{config.cluster_name}"
        )

    @property
    def kube_context(self) -> str:
        """Returns the kubectl context name for this KinD cluster."""
        return f"kind-{self.config.cluster_name}"

    def check_prerequisites(self, required_tools: list[str] | None = None) -> None:
        """Validates host tools necessary to provision and manage KinD clusters."""
        check_preflight_tools(required_tools)

    def exists(self) -> bool:
        """Checks if the configured KinD cluster exists."""
        return self.cluster_exists(self.config.cluster_name)

    def cluster_exists(self, cluster_name: str | None = None) -> bool:
        """Checks if a specified KinD cluster exists."""
        target_name = cluster_name or self.config.cluster_name
        res = run_command(
            ["kind", "get", "clusters"],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        if res.returncode != 0:
            return False
        clusters = [c.strip() for c in res.stdout.splitlines() if c.strip()]
        exists = target_name in clusters
        _LOGGER.debug(
            "Checked KinD cluster '%s' existence: %s (all: %s)", target_name, exists, clusters
        )
        return exists

    def _resolve_manifests_dir(self) -> Path:
        """Locates the test/e2e manifests directory containing KinD configs."""
        for c in [
            self.repo_root / "test" / "e2e" / "manifests",
            self.repo_root / "deploy" / "local" / "manifests",
        ]:
            if c.is_dir():
                _LOGGER.debug("Resolved manifests dir: %s", c)
                return c
        res = self.repo_root / "test" / "e2e" / "manifests"
        _LOGGER.debug("Defaulted manifests dir: %s", res)
        return res

    def create_cluster(self, cluster_name: str | None = None, recreate: bool = False) -> None:
        """Creates the KinD cluster with NodePort port-mappings if not already running."""
        name = cluster_name or self.config.cluster_name
        if self.cluster_exists(name):
            if recreate:
                log_info(f"Cluster '{name}' already exists, deleting for recreate...")
                self.delete_cluster(name)
            else:
                _LOGGER.debug("KinD cluster '%s' already exists. Reusing cluster context.", name)
                log_info(f"KinD cluster '{name}' already exists. Reusing cluster.")
                run_command(["kubectl", "config", "use-context", f"kind-{name}"])
                return

        kind_config = self._resolve_manifests_dir() / "kind_config.yaml"
        if not kind_config.is_file():
            raise FileNotFoundError(f"KinD cluster config not found at '{kind_config}'")

        log_info(f"=== [KinD] Provisioning cluster '{name}' using config '{kind_config}' ===")
        run_command(["kind", "create", "cluster", "--name", name, f"--config={kind_config}"])
        run_command(["kubectl", "config", "use-context", f"kind-{name}"])
        log_info("Ensuring Kubernetes namespace 'xmc' exists...")
        run_command(
            ["kubectl", "--context", f"kind-{name}", "create", "namespace", "xmc"],
            check=False,
        )
        log_success(f"KinD cluster '{name}' is ready.")

    def _resolve_adc_file(self) -> Path:
        """Resolves path to valid Google Application Default Credentials JSON file."""
        adc_path = os.environ.get("GOOGLE_APPLICATION_CREDENTIALS")
        if adc_path and Path(adc_path).is_file():
            _LOGGER.debug("Resolved ADC from GOOGLE_APPLICATION_CREDENTIALS: %s", adc_path)
            return Path(adc_path).resolve()
        default_adc = Path.home() / ".config" / "gcloud" / "application_default_credentials.json"
        if default_adc.is_file():
            _LOGGER.debug("Resolved ADC from default gcloud path: %s", default_adc)
            return default_adc.resolve()
        if "SPANNER_EMULATOR_HOST" in os.environ:
            self.temp_dir.mkdir(parents=True, exist_ok=True)
            self.temp_dir.chmod(0o700)
            dummy_path = self.temp_dir / "dummy-credentials.json"
            if not dummy_path.exists():
                dummy_path.write_text('{"type": "service_account", "project_id": "dummy"}')
            _LOGGER.debug("Resolved dummy ADC for emulator: %s", dummy_path)
            return dummy_path
        raise FileNotFoundError(
            "GCP Application Default Credentials (ADC) file not found. "
            "Please run 'gcloud auth application-default login' or set GOOGLE_APPLICATION_CREDENTIALS."
        )

    def ensure_secrets(self, cluster_name: str | None = None) -> None:
        """Creates GCP service account credentials, OAuth tokens, and TLS certificates."""
        name = cluster_name or self.config.cluster_name
        ctx = f"kind-{name}"
        log_info("=== [KinD] Configuring Kubernetes secrets in 'xmc' namespace ===")
        self.temp_dir.mkdir(parents=True, exist_ok=True)
        self.temp_dir.chmod(0o700)
        adc_file = self._resolve_adc_file()

        log_info("Creating GCP SA credentials secret 'gcp-sa-key'...")
        self._apply_secret_generic(
            "gcp-sa-key",
            ["xmc"],
            files=[f"key.json={adc_file}"],
            ctx=ctx,
        )

        log_info("Creating backend OAuth secrets 'backend-oauth-secrets'...")
        self._apply_secret_generic(
            "backend-oauth-secrets",
            ["xmc"],
            files=[f"key.json={adc_file}"],
            literals=[
                "jwt-secret=e2e-development-secret-must-be-at-least-32-chars-long",
                "client-secret=xmc-client-secret",
            ],
            ctx=ctx,
        )

        log_info("Generating self-signed TLS certificates for 'validation-secret'...")
        tls_key, tls_crt = (
            self.temp_dir / "dev-validation.key",
            self.temp_dir / "dev-validation.crt",
        )
        if not tls_key.exists() or not tls_crt.exists():
            run_command(
                [
                    "openssl",
                    "genpkey",
                    "-algorithm",
                    "RSA",
                    "-out",
                    str(tls_key),
                    "-pkeyopt",
                    "rsa_keygen_bits:2048",
                ]
            )
            run_command(
                [
                    "openssl",
                    "req",
                    "-new",
                    "-x509",
                    "-key",
                    str(tls_key),
                    "-out",
                    str(tls_crt),
                    "-days",
                    "365",
                    "-subj",
                    "/CN=xmc-dev-signer",
                ]
            )

        self._apply_secret_generic(
            "validation-secret",
            ["xmc"],
            files=[f"tls.key={tls_key}", f"tls.crt={tls_crt}"],
            ctx=ctx,
        )

        log_info("Creating Redis secret 'redis-secret'...")
        self._apply_secret_generic("redis-secret", ["xmc"], literals=["redis-password="], ctx=ctx)
        log_success("Kubernetes secrets successfully configured.")

    def create_secrets(self, cluster_name: str | None = None) -> None:
        """Alias for ensure_secrets."""
        self.ensure_secrets(cluster_name)

    def _apply_secret_generic(
        self,
        name: str,
        namespaces: list[str],
        files: list[str] | None = None,
        literals: list[str] | None = None,
        ctx: str | None = None,
    ) -> None:
        """Applies generic secret across namespaces idempotently."""
        target_ctx = ctx or self.kube_context
        for ns in namespaces:
            run_command(
                [
                    "kubectl",
                    "--context",
                    target_ctx,
                    "delete",
                    "secret",
                    name,
                    "-n",
                    ns,
                    "--ignore-not-found",
                ],
                check=False,
                log_cmd=False,
            )
            create_cmd = [
                "kubectl",
                "--context",
                target_ctx,
                "create",
                "secret",
                "generic",
                name,
                "-n",
                ns,
            ]
            if files:
                for f in files:
                    create_cmd.append(f"--from-file={f}")
            if literals:
                for lit in literals:
                    create_cmd.append(f"--from-literal={lit}")
            run_command(create_cmd, check=True)

    def delete_cluster(self, cluster_name: str | None = None) -> None:
        """Deletes the local KinD development cluster."""
        name = cluster_name or self.config.cluster_name
        if not self.cluster_exists(name):
            log_info(f"KinD cluster '{name}' does not exist.")
            return

        log_info(f"=== [KinD] Deleting cluster '{name}' ===")
        run_command(["kind", "delete", "cluster", "--name", name])
        if self.temp_dir.exists():
            shutil.rmtree(self.temp_dir, ignore_errors=True)
        log_success(f"KinD cluster '{name}' deleted.")
