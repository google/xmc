#!/usr/bin/env python3
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

"""Modular Python orchestrator for XMC Non-Agentic Headless E2E Testing."""

from __future__ import annotations

import argparse
import contextlib
import dataclasses
import json
import logging
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Generator
from typing import Any

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger("xmc_e2e_runner")


@dataclasses.dataclass
class E2EConfig:
    """Configuration options for the E2E test lifecycle."""

    project_id: str
    spanner_instance: str
    cluster_name: str
    kind_bin: str
    workspace_root: str
    screenshots_dir: str
    keep_cluster: bool = False
    skip_build: bool = False
    ess_db: str = ""
    xid_db: str = ""
    temp_dir: str = ""

    def __post_init__(self) -> None:
        ts = int(time.time())
        if not self.ess_db:
            self.ess_db = f"e2e-ess-{ts}"
        if not self.xid_db:
            self.xid_db = f"e2e-xid-{ts}"
        if not self.temp_dir:
            self.temp_dir = tempfile.mkdtemp(prefix="xmc_e2e_")


def run_cmd(
    cmd: list[str],
    cwd: str | None = None,
    env: dict[str, str] | None = None,
    timeout: float | None = None,
    check: bool = True,
    capture_output: bool = False,
) -> subprocess.CompletedProcess[str]:
    """Runs a subprocess command with clear logging and timeout bounds."""
    logger.info("Executing: %s", " ".join(cmd))
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        timeout=timeout,
        check=check,
        capture_output=capture_output,
        text=True,
    )


class SpannerManager:
    """Manages ephemeral Cloud Spanner databases and migrations."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def _cleanup_stale_databases(self) -> None:
        """Deletes stale ephemeral test databases to stay under quota limits."""
        logger.info(
            "Checking for stale ephemeral databases on instance %s...",
            self.cfg.spanner_instance,
        )
        try:
            res = run_cmd(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "list",
                    f"--instance={self.cfg.spanner_instance}",
                    f"--project={self.cfg.project_id}",
                    "--format=value(NAME)",
                ],
                capture_output=True,
                check=False,
            )
            current_ts = int(time.time())
            for line in res.stdout.splitlines():
                db = line.strip()
                if not db.startswith("e2e-ess-") and not db.startswith("e2e-xid-"):
                    continue
                try:
                    ts_part = int(db.split("-")[-1])
                    # If older than 15 minutes, remove it
                    if current_ts - ts_part > 900:
                        logger.info("Cleaning up stale database: %s", db)
                        run_cmd(
                            [
                                "gcloud",
                                "spanner",
                                "databases",
                                "delete",
                                db,
                                f"--instance={self.cfg.spanner_instance}",
                                f"--project={self.cfg.project_id}",
                                "--quiet",
                            ],
                            check=False,
                        )
                except ValueError:
                    continue
        except Exception as e:
            logger.warning("Stale database cleanup failed: %s", e)

    def setup(self) -> None:
        """Creates ephemeral databases and runs migrations."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        for db_name, mig_dir in [
            (self.cfg.ess_db, "ess"),
            (self.cfg.xid_db, "xid"),
        ]:
            logger.info("Creating Spanner database %s...", db_name)
            run_cmd(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "create",
                    db_name,
                    f"--instance={self.cfg.spanner_instance}",
                    f"--project={self.cfg.project_id}",
                ]
            )
            logger.info("Applying migrations for %s (%s)...", db_name, mig_dir)
            for attempt in range(1, 4):
                try:
                    run_cmd(
                        [
                            "bazel",
                            "run",
                            "//internal/storage/spanner/migrations:migrations",
                            "--",
                            f"-project={self.cfg.project_id}",
                            f"-instance={self.cfg.spanner_instance}",
                            f"-database={db_name}",
                            f"-migrations_dir={mig_dir}",
                            "-command=up",
                        ],
                        cwd=self.cfg.workspace_root,
                    )
                    break
                except Exception as e:
                    if attempt == 3:
                        raise
                    logger.warning("Migration attempt %d failed: %s. Retrying in 5s...", attempt, e)
                    time.sleep(5)

    def teardown(self) -> None:
        """Drops ephemeral databases safely."""
        logger.info("=== [Spanner] Cleaning up ephemeral databases ===")
        for db_name in [self.cfg.ess_db, self.cfg.xid_db]:
            try:
                logger.info("Dropping Spanner database %s...", db_name)
                run_cmd(
                    [
                        "gcloud",
                        "spanner",
                        "databases",
                        "delete",
                        db_name,
                        f"--instance={self.cfg.spanner_instance}",
                        f"--project={self.cfg.project_id}",
                        "--quiet",
                    ],
                    check=False,
                )
            except Exception as e:
                logger.warning("Failed to drop database %s: %s", db_name, e)


class KindClusterManager:
    """Manages ephemeral Kubernetes in Docker (KinD) cluster."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def exists(self) -> bool:
        """Checks if the configured KinD cluster exists."""
        res = run_cmd([self.cfg.kind_bin, "get", "clusters"], capture_output=True, check=False)
        return self.cfg.cluster_name in res.stdout.splitlines()

    def setup(self) -> None:
        """Creates KinD cluster and applies manifests."""
        logger.info("=== [KinD] Provisioning cluster %s ===", self.cfg.cluster_name)
        if self.exists():
            logger.info("Cluster %s already exists, deleting first...", self.cfg.cluster_name)
            run_cmd(
                [self.cfg.kind_bin, "delete", "cluster", "--name", self.cfg.cluster_name],
                check=False,
            )

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

        logger.info("Creating namespace 'xmc' and service account secret...")
        run_cmd(["kubectl", "create", "namespace", "xmc"])

        adc_file = os.environ.get(
            "GOOGLE_APPLICATION_CREDENTIALS",
            os.path.expanduser("~/.config/gcloud/application_default_credentials.json"),
        )
        is_service_account_json = False
        if os.path.isfile(adc_file):
            try:
                with open(adc_file, encoding="utf-8") as cf:
                    cred_content = json.load(cf)
                if cred_content.get("type") == "service_account":
                    is_service_account_json = True
            except Exception as e:
                logger.debug("Could not parse credential file %s: %s", adc_file, e)

        if is_service_account_json:
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
        else:
            dummy_key_path = os.path.join(self.cfg.temp_dir, "dummy_sa_key.json")
            with open(dummy_key_path, "w", encoding="utf-8") as f:
                f.write('{"type": "service_account", "project_id": "dummy"}\n')
            run_cmd(
                [
                    "kubectl",
                    "create",
                    "secret",
                    "generic",
                    "gcp-sa-key",
                    "-n",
                    "xmc",
                    f"--from-file=key.json={dummy_key_path}",
                ]
            )

        logger.info("Creating backend secrets...")
        secrets_cmd = [
            "kubectl",
            "create",
            "secret",
            "generic",
            "backend-oauth-secrets",
            "-n",
            "xmc",
            "--from-literal=jwt-secret=e2e-development-secret-must-be-at-least-32-chars-long",
            "--from-literal=client-secret=xmc-client-secret",
        ]
        if is_service_account_json:
            secrets_cmd.append(f"--from-file=key.json={adc_file}")
        else:
            dummy_key_path = os.path.join(self.cfg.temp_dir, "dummy_sa_key.json")
            secrets_cmd.append(f"--from-file=key.json={dummy_key_path}")
        run_cmd(secrets_cmd)

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

        logger.info("Applying dummy CRDs, Redis, and Gateway Proxy...")
        run_cmd(["kubectl", "apply", "-f", os.path.join(manifests_dir, "dummy_crds.yaml")])
        run_cmd(["kubectl", "apply", "-f", os.path.join(manifests_dir, "redis.yaml")])
        run_cmd(["kubectl", "apply", "-f", os.path.join(manifests_dir, "dex.yaml")])
        run_cmd(["kubectl", "apply", "-f", os.path.join(manifests_dir, "gateway_proxy.yaml")])

    def load_images(self, images: list[str]) -> None:
        """Loads Docker images into KinD nodes."""
        logger.info("=== [KinD] Loading container images into cluster ===")
        for img in images:
            logger.info("Loading image %s...", img)
            run_cmd(
                [self.cfg.kind_bin, "load", "docker-image", img, "--name", self.cfg.cluster_name]
            )

    def teardown(self) -> None:
        """Destroys the KinD cluster."""
        if not self.cfg.keep_cluster:
            try:
                if self.exists():
                    logger.info("=== [KinD] Deleting cluster %s ===", self.cfg.cluster_name)
                    run_cmd(
                        [self.cfg.kind_bin, "delete", "cluster", "--name", self.cfg.cluster_name],
                        check=False,
                    )
            except Exception as e:
                logger.warning("Failed to safely teardown KinD cluster: %s", e)


class ImageBuilder:
    """Builds and tags OCI container images via Bazel and Docker."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def build_all(self) -> list[str]:
        """Builds all backend and frontend images."""
        if self.cfg.skip_build:
            logger.info("=== [Bazel] Skipping image builds (--skip-build set) ===")
            return [
                "experiment_state_server_image:latest",
                "xid_service_image:latest",
                "ui-image:latest",
                "actuator_image:latest",
                "reconciler_image:latest",
            ]

        logger.info("=== [Bazel] Building OCI images ===")
        services = [
            (
                "cmd/experiment_state_server",
                "experiment_state_server_image_tarball",
                "experiment_state_server_image:latest",
            ),
            ("cmd/xid_service", "xid_service_image_tarball", "xid_service_image:latest"),
            ("cmd/ui", "ui_image_tarball", "ui-image:latest"),
            ("cmd/actuator", "actuator_image_tarball", "actuator_image:latest"),
            ("cmd/reconciler", "reconciler_image_tarball", "reconciler_image:latest"),
        ]

        logger.info("Building all service images in parallel with Bazel...")
        bazel_targets = [f"//{pkg}:{target}" for pkg, target, _ in services]
        run_cmd(["bazel", "build", *bazel_targets], cwd=self.cfg.workspace_root)

        images: list[str] = []
        for pkg, target, tag in services:
            tar_path = os.path.join(self.cfg.workspace_root, "bazel-bin", pkg, f"{target}.tar")
            run_cmd(["docker", "load", "-i", tar_path])
            images.append(tag)

        return images


class HelmDeployer:
    """Manages Helm release installation on the KinD cluster."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.helm_dir = os.path.join(self.cfg.temp_dir, "helm")
        self.values_file = os.path.join(self.cfg.temp_dir, "values.yaml")

    def _prepare_helm_chart(self) -> None:
        """Copies chart and removes cloud-only resources incompatible with local KinD."""
        if os.path.exists(self.helm_dir):
            shutil.rmtree(self.helm_dir)
        shutil.copytree(os.path.join(self.cfg.workspace_root, "deploy", "helm"), self.helm_dir)

        # Remove templates that require GKE-specific CRDs not present in local KinD
        for prune_target in [
            "certs",
            "kueue",
            "kueue-dev",
            "wpc",
            "dashboard-service",
            "health-service",
            "ingestion-gateway",
            "rod",
            "ess/gateway.yaml",
            "ess/httproute.yaml",
            "ess/grpc-backend-config.yaml",
            "ess/grpc-healthcheck-policy.yaml",
            "ess/managed-certificate.yaml",
            "ess/bootstrap-tls.yaml",
            "ess/secretproviderclass.yaml",
            "ess/hpa.yaml",
            "xid/hpa.yaml",
            "actuator/vpa.yaml",
            "reconciler/vpa.yaml",
        ]:
            path = os.path.join(self.helm_dir, "templates", prune_target)
            if os.path.isdir(path):
                shutil.rmtree(path)
            elif os.path.isfile(path):
                os.remove(path)

    def _patch_image_pull_policy(self) -> None:
        """Replaces imagePullPolicy: Always with IfNotPresent so KinD uses local images."""
        for root, _, files in os.walk(self.helm_dir):
            for file in files:
                if file.endswith(".yaml") or file.endswith(".tpl"):
                    file_path = os.path.join(root, file)
                    with open(file_path, encoding="utf-8") as f:
                        content = f.read()
                    if "imagePullPolicy: Always" in content:
                        content = content.replace(
                            "imagePullPolicy: Always", "imagePullPolicy: IfNotPresent"
                        )
                        with open(file_path, "w", encoding="utf-8") as f:
                            f.write(content)

    def _generate_values_file(self) -> None:
        """Generates dynamic Helm values connecting to local Dex and Spanner."""
        values_content = f"""
projectId: "{self.cfg.project_id}"
environment: "dev"
domainSuffix: "e2e.xmc.local"
isDevEnvironment: true
imageTag: latest
gcpServiceAccount: "xmc-dev-sa@{self.cfg.project_id}.iam.gserviceaccount.com"
taskSignatureTtlSeconds: 300
statusMessageHistoryEnabled: false
redisAddr: "redis-service.xmc.svc.cluster.local:6379"

xid_custom_image: "xid_service_image:latest"
actuator_custom_image: "actuator_image:latest"
reconciler_custom_image: "reconciler_image:latest"
ess_custom_image: "experiment_state_server_image:latest"

experimentStateServer:
  spannerDatabaseName: "projects/{self.cfg.project_id}/instances/{self.cfg.spanner_instance}/databases/{self.cfg.ess_db}"

xidService:
  spannerDatabaseName: "projects/{self.cfg.project_id}/instances/{self.cfg.spanner_instance}/databases/{self.cfg.xid_db}"
  hpa:
    minReplicas: 1
    maxReplicas: 1

essDeployment:
  enabled: true
  hpa:
    minReplicas: 1
    maxReplicas: 1

auth:
  disableAuthentication: false
  isNonProdAuth: true
  allowedEmails: "admin@xmc.local"
  clientId: "xmc-client"
  iap:
    clientId: "xmc-client"
    iapIssuer: "http://dex.dex.svc.cluster.local:5556/dex"
  googleApplicationCredentials: "/etc/gcp-secrets/key.json"
  redirectUrl: "http://localhost:8080/api/callback"
  frontendUrl: "http://localhost:8080"
  authUrl: "http://localhost:5556/dex/auth"
  tokenUrl: "http://dex.dex.svc.cluster.local:5556/dex/token"
  apiUrl: "http://dex.dex.svc.cluster.local:5556/dex/userinfo"
  secureCookie: false
  issuerUrl: "http://dex.dex.svc.cluster.local:5556/dex"

secrets:
  csi:
    enabled: false
  development:
    jwtSecret: "e2e-development-secret-must-be-at-least-32-chars-long"
    clientSecret: "xmc-client-secret"

ui:
  enabled: true
  image: "ui-image:latest"
  apiUrl: "http://localhost:8080/v1"
"""
        with open(self.values_file, "w", encoding="utf-8") as vf:
            vf.write(values_content)

    def _wait_for_rollouts(self) -> None:
        """Waits for deployments across namespaces to complete rollouts."""
        logger.info("Waiting for core pods to reach Ready status...")
        run_cmd(["kubectl", "rollout", "status", "deployment/dex", "-n", "dex", "--timeout=180s"])
        try:
            for deploy in [
                "gateway-proxy",
                "xid-service-deployment",
                "experiment-state-server-deployment",
                "ui-deployment",
                "actuator-deployment",
                "reconciler-deployment",
            ]:
                run_cmd(
                    [
                        "kubectl",
                        "rollout",
                        "status",
                        f"deployment/{deploy}",
                        "-n",
                        "xmc",
                        "--timeout=180s",
                    ]
                )
        except Exception:
            logger.error("Deployment rollout failed! Capturing diagnostics...")
            diag_file = os.path.join(self.cfg.screenshots_dir, "k8s_diagnostics.log")
            os.makedirs(self.cfg.screenshots_dir, exist_ok=True)
            with open(diag_file, "w", encoding="utf-8") as df:
                for ns in ["xmc", "dex"]:
                    df.write(f"=== PODS (ns: {ns}) ===\n")
                    pods_res = run_cmd(
                        ["kubectl", "get", "pods", "-n", ns, "-o", "wide"],
                        check=False,
                        capture_output=True,
                    )
                    df.write(pods_res.stdout + "\n" + pods_res.stderr + "\n")
                    df.write(f"=== EVENTS (ns: {ns}) ===\n")
                    events_res = run_cmd(
                        [
                            "kubectl",
                            "get",
                            "events",
                            "-n",
                            ns,
                            "--sort-by=.metadata.creationTimestamp",
                        ],
                        check=False,
                        capture_output=True,
                    )
                    df.write(events_res.stdout + "\n" + events_res.stderr + "\n")
                    df.write(f"=== ALL LOGS (ns: {ns}) ===\n")
                    pods_list_res = run_cmd(
                        [
                            "kubectl",
                            "get",
                            "pods",
                            "-n",
                            ns,
                            "-o",
                            "jsonpath={.items[*].metadata.name}",
                        ],
                        check=False,
                        capture_output=True,
                    )
                    pod_names = pods_list_res.stdout.strip().split()
                    for pod_name in pod_names:
                        df.write(f"--- Logs for pod: {pod_name} ---\n")
                        logs_res = run_cmd(
                            [
                                "kubectl",
                                "logs",
                                "-n",
                                ns,
                                pod_name,
                                "--all-containers=true",
                                "--tail=100",
                            ],
                            check=False,
                            capture_output=True,
                        )
                        df.write(logs_res.stdout + "\n" + logs_res.stderr + "\n")
            logger.info("Diagnostics saved to %s", diag_file)
            raise

    def deploy(self) -> None:
        """Copies chart, generates values, and executes helm upgrade --install."""
        logger.info("=== [Helm] Deploying XMC Stack ===")
        self._prepare_helm_chart()
        self._patch_image_pull_policy()
        self._generate_values_file()

        run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "xmc",
                self.helm_dir,
                "--kube-context",
                f"kind-{self.cfg.cluster_name}",
                "-n",
                "xmc",
                "-f",
                self.values_file,
            ]
        )
        self._wait_for_rollouts()


class PlaywrightTestExecutor:
    """Executes the Playwright headless test script inside a virtual environment."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.venv_dir = os.path.join(self.cfg.temp_dir, "venv")

    def setup_venv(self) -> str:
        """Sets up Python virtual environment and installs dependencies."""
        python_bin = sys.executable
        if not os.path.exists(os.path.join(self.venv_dir, "bin", "python")):
            logger.info("Creating Python virtualenv at %s...", self.venv_dir)
            run_cmd([python_bin, "-m", "venv", self.venv_dir])
            run_cmd(
                [
                    os.path.join(self.venv_dir, "bin", "pip"),
                    "install",
                    "--upgrade",
                    "pip",
                    "playwright",
                    "requests",
                ]
            )
            run_cmd([os.path.join(self.venv_dir, "bin", "playwright"), "install", "chromium"])
        return os.path.join(self.venv_dir, "bin", "python")

    def execute(self) -> None:
        """Runs the test_e2e_flow.py script."""
        logger.info("=== [Playwright] Executing E2E Test Flow ===")
        py_venv = self.setup_venv()
        test_script = os.path.join(self.cfg.workspace_root, "test", "e2e", "test_e2e_flow.py")
        env = os.environ.copy()
        env["XMC_BASE_URL"] = "http://localhost:8080"
        env["XMC_DEX_URL"] = "http://localhost:5556"
        env["XMC_SCREENSHOTS_DIR"] = self.cfg.screenshots_dir

        run_cmd([py_venv, test_script], cwd=self.cfg.workspace_root, env=env)


class E2EOrchestrator:
    """Coordinates the full E2E setup, execution, and teardown."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.spanner = SpannerManager(config)
        self.kind = KindClusterManager(config)
        self.builder = ImageBuilder(config)
        self.helm = HelmDeployer(config)
        self.tester = PlaywrightTestExecutor(config)

    @contextlib.contextmanager
    def managed_lifecycle(self) -> Generator[None, None, None]:
        """Guarantees cleanup on success, failure, or interrupt."""
        try:
            self.spanner.setup()
            self.kind.setup()
            yield
        finally:
            logger.info("=== Executing Teardown ===")
            self.kind.teardown()
            if not self.cfg.keep_cluster:
                self.spanner.teardown()
            else:
                logger.info(
                    "[Spanner] Preserving ephemeral databases %s and %s for debugging.",
                    self.cfg.ess_db,
                    self.cfg.xid_db,
                )
            if not self.cfg.keep_cluster and os.path.exists(self.cfg.temp_dir):
                logger.info("Cleaning up temporary directory %s...", self.cfg.temp_dir)
                shutil.rmtree(self.cfg.temp_dir, ignore_errors=True)

    def run(self) -> None:
        """Executes the full end-to-end lifecycle."""
        start_time = time.time()
        logger.info("================================================================")
        logger.info(" Starting XMC Headless E2E Test Suite")
        logger.info(" Cluster:       %s", self.cfg.cluster_name)
        logger.info(" Project:       %s", self.cfg.project_id)
        logger.info(" Instance:      %s", self.cfg.spanner_instance)
        logger.info(" ESS Database:  %s", self.cfg.ess_db)
        logger.info(" XID Database:  %s", self.cfg.xid_db)
        logger.info(" Screenshots:   %s", self.cfg.screenshots_dir)
        logger.info("================================================================")

        def _signal_handler(signum: int, _frame: Any) -> None:
            logger.warning("Received signal %d. Triggering cleanup and exiting...", signum)
            sys.exit(1)

        signal.signal(signal.SIGINT, _signal_handler)
        signal.signal(signal.SIGTERM, _signal_handler)

        with self.managed_lifecycle():
            images = self.builder.build_all()
            self.kind.load_images(images)
            self.helm.deploy()
            self.tester.execute()

        elapsed = time.time() - start_time
        logger.info("================================================================")
        logger.info("🎉 E2E Test Suite Succeeded in %.2f seconds! 🎉", elapsed)
        logger.info("================================================================")


def parse_args() -> E2EConfig:
    """Parses command-line arguments and constructs E2EConfig."""
    parser = argparse.ArgumentParser(description="Run XMC Headless E2E Tests.")
    parser.add_argument(
        "--project",
        default=os.getenv("GCP_PROJECT_ID", "xmc-test-project-3"),
        help="Google Cloud Project ID (default: xmc-test-project-3)",
    )
    parser.add_argument(
        "--spanner-instance",
        default=os.getenv("SPANNER_INSTANCE", "xmc-e2e"),
        help="Spanner instance ID (default: xmc-e2e)",
    )
    parser.add_argument(
        "--cluster-name",
        default=os.getenv("KIND_CLUSTER_NAME", "xmc-e2e"),
        help="KinD cluster name (default: xmc-e2e)",
    )
    parser.add_argument(
        "--kind-bin",
        default=shutil.which("kind") or "kind",
        help="Path to kind executable",
    )
    parser.add_argument(
        "--screenshots-dir",
        default=os.getenv(
            "XMC_SCREENSHOTS_DIR",
            os.path.join(tempfile.gettempdir(), "xmc_e2e_screenshots"),
        ),
        help="Directory to save Playwright screenshots",
    )
    parser.add_argument(
        "--keep-cluster",
        action="store_true",
        help="Keep KinD cluster alive after test completion for debugging",
    )
    parser.add_argument(
        "--skip-build",
        action="store_true",
        help="Skip Bazel image compilation if images are already built and loaded",
    )

    args = parser.parse_args()
    workspace_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

    return E2EConfig(
        project_id=args.project,
        spanner_instance=args.spanner_instance,
        cluster_name=args.cluster_name,
        kind_bin=args.kind_bin,
        workspace_root=workspace_root,
        screenshots_dir=args.screenshots_dir,
        keep_cluster=args.keep_cluster,
        skip_build=args.skip_build,
    )


def main() -> None:
    """Entrypoint function for runner execution."""
    config = parse_args()
    orchestrator = E2EOrchestrator(config)
    orchestrator.run()


if __name__ == "__main__":
    main()
