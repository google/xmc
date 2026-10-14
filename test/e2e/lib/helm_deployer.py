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

"""Helm chart preparation, values configuration, and KinD stack deployment."""

from __future__ import annotations

import logging
import os
import re
import shutil
import subprocess
from typing import TextIO

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.controller_installer import ControllerInstaller

logger = logging.getLogger("xmc_e2e_runner")

# Namespaces whose pods, events, and logs are captured when the deployment or the tests fail.
_DIAGNOSTIC_NAMESPACES = ("xmc", "dex", "jobset-system", "kueue-system", "cpu-queue")


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
        shutil.copytree(
            os.path.join(self.cfg.workspace_root, "deploy", "helm"),
            self.helm_dir,
        )

        # Remove templates that require GKE-specific CRDs not present in local KinD
        for prune_target in [
            "certs",
            "kueue",
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
                if file.endswith((".yaml", ".tpl")):
                    file_path = os.path.join(root, file)
                    with open(file_path, encoding="utf-8") as f:
                        content = f.read()
                    patched_content, count = re.subn(
                        r'imagePullPolicy:\s*["\']?Always["\']?',
                        "imagePullPolicy: IfNotPresent",
                        content,
                    )
                    if count > 0:
                        with open(file_path, "w", encoding="utf-8") as f:
                            f.write(patched_content)

    def _generate_values_file(self) -> None:
        """Generates dynamic Helm values connecting to local Dex and Spanner."""
        values_content = f"""
projectId: "{self.cfg.project_id}"
environment: "dev"
domainSuffix: "e2e.xmc.local"
isDevEnvironment: true
imageTag: latest
gcpServiceAccount: "admin@xmc.local"
taskSignatureTtlSeconds: 300
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
  allowedEmails: "admin@xmc.local,viewer@xmc.local"
  clientId: "xmc-client"
  iap:
    clientId: "xmc-client"
    iapIssuer: "http://localhost:5556/dex"
  googleApplicationCredentials: "/etc/gcp-secrets/key.json"
  redirectUrl: "http://localhost:8080/api/callback"
  frontendUrl: "http://localhost:8080"
  authUrl: "http://localhost:5556/dex/auth"
  tokenUrl: "http://dex.dex.svc.cluster.local:5556/dex/token"
  apiUrl: "http://dex.dex.svc.cluster.local:5556/dex/userinfo"
  jwksUrl: "http://dex.dex.svc.cluster.local:5556/dex/keys"
  secureCookie: false
  issuerUrl: "http://localhost:5556/dex"
  username: "admin@xmc.local"
  password: "password123"

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
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        with open(self.values_file, "w", encoding="utf-8") as vf:
            vf.write(values_content)

    def capture_diagnostics(self) -> None:
        """Captures pod status, cluster events, and container logs after a failure.

        Best effort: capture failures are logged and never mask the failure being diagnosed.
        """
        logger.error("Capturing cluster diagnostics after failure...")
        diag_file = os.path.join(self.cfg.screenshots_dir, "k8s_diagnostics.log")
        try:
            os.makedirs(self.cfg.screenshots_dir, exist_ok=True)
            with open(diag_file, "w", encoding="utf-8") as df:
                for ns in _DIAGNOSTIC_NAMESPACES:
                    self._write_namespace_diagnostics(df, ns)
            logger.info("Diagnostics saved to %s", diag_file)
        except Exception as e:
            logger.warning("Failed to capture cluster diagnostics: %s", e)

    def _write_namespace_diagnostics(self, df: TextIO, ns: str) -> None:
        """Writes the pods, events, and recent container logs of a namespace to df."""
        df.write(f"=== PODS (ns: {ns}) ===\n")
        df.write(self._kubectl_report("get", "pods", "-n", ns, "-o", "wide"))
        df.write(f"=== EVENTS (ns: {ns}) ===\n")
        df.write(
            self._kubectl_report("get", "events", "-n", ns, "--sort-by=.metadata.creationTimestamp")
        )
        df.write(f"=== ALL LOGS (ns: {ns}) ===\n")
        pods_list_res = self._run_kubectl_best_effort(
            "get", "pods", "-n", ns, "-o", "jsonpath={.items[*].metadata.name}"
        )
        for pod_name in pods_list_res.stdout.strip().split():
            df.write(f"--- Logs for pod: {pod_name} ---\n")
            df.write(
                self._kubectl_report(
                    "logs", "-n", ns, pod_name, "--all-containers=true", "--tail=100"
                )
            )

    def _kubectl_report(self, *args: str) -> str:
        """Returns the stdout and stderr of a best-effort kubectl command as a report block."""
        result = self._run_kubectl_best_effort(*args)
        return result.stdout + "\n" + result.stderr + "\n"

    def _run_kubectl_best_effort(self, *args: str) -> subprocess.CompletedProcess[str]:
        """Runs a kubectl command against the KinD cluster without raising on failure."""
        context = f"kind-{self.cfg.cluster_name}"
        return run_cmd(["kubectl", "--context", context, *args], check=False, capture_output=True)

    def _wait_for_rollouts(self) -> None:
        """Waits for deployments across namespaces to complete rollouts."""
        logger.info("Waiting for core pods to reach Ready status...")
        context = f"kind-{self.cfg.cluster_name}"
        try:
            run_cmd(
                [
                    "kubectl",
                    "--context",
                    context,
                    "rollout",
                    "status",
                    "deployment/dex",
                    "-n",
                    "dex",
                    "--timeout=180s",
                ]
            )
            for deploy in [
                "xid-service-deployment",
                "experiment-state-server-deployment",
                "actuator-deployment",
                "reconciler-deployment",
                "ui-deployment",
            ]:
                run_cmd(
                    [
                        "kubectl",
                        "--context",
                        context,
                        "rollout",
                        "status",
                        f"deployment/{deploy}",
                        "-n",
                        "xmc",
                        "--timeout=180s",
                    ]
                )
            # Restart gateway-proxy now that all backend services and UI are fully ready
            run_cmd(
                [
                    "kubectl",
                    "--context",
                    context,
                    "rollout",
                    "restart",
                    "deployment/gateway-proxy",
                    "-n",
                    "xmc",
                ],
                check=False,
            )
            run_cmd(
                [
                    "kubectl",
                    "--context",
                    context,
                    "rollout",
                    "status",
                    "deployment/gateway-proxy",
                    "-n",
                    "xmc",
                    "--timeout=180s",
                ]
            )
        except Exception:
            self.capture_diagnostics()
            raise

    def _install_controllers(self) -> None:
        """Installs the JobSet and Kueue controllers, capturing diagnostics on failure.

        Kueue's webhooks fail closed for pods and deployments in the XMC namespaces, so the
        controllers must serve before the Helm release creates any workloads.
        """
        try:
            ControllerInstaller(self.cfg).install_all()
        except Exception:
            self.capture_diagnostics()
            raise

    def deploy(self) -> None:
        """Copies chart, generates values, installs controllers, and runs helm upgrade --install."""
        logger.info("=== [Helm] Deploying XMC Stack ===")
        self._prepare_helm_chart()
        self._patch_image_pull_policy()
        self._generate_values_file()
        self._install_controllers()

        context = f"kind-{self.cfg.cluster_name}"
        run_cmd(
            [
                "helm",
                "upgrade",
                "--install",
                "xmc",
                self.helm_dir,
                "--kube-context",
                context,
                "-n",
                "xmc",
                "-f",
                self.values_file,
            ]
        )
        self._wait_for_rollouts()
