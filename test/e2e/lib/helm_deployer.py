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

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


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
    iapIssuer: "http://dex.dex.svc.cluster.local:5556/dex"
  googleApplicationCredentials: "/etc/gcp-secrets/key.json"
  redirectUrl: "http://localhost:8080/api/callback"
  frontendUrl: "http://localhost:8080"
  authUrl: "http://localhost:5556/dex/auth"
  tokenUrl: "http://dex.dex.svc.cluster.local:5556/dex/token"
  apiUrl: "http://dex.dex.svc.cluster.local:5556/dex/userinfo"
  secureCookie: false
  issuerUrl: "http://dex.dex.svc.cluster.local:5556/dex"
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

    def _capture_diagnostics(self) -> None:
        """Captures pod status, cluster events, and container logs upon rollout failure."""
        logger.error("Deployment rollout failed! Capturing diagnostics...")
        diag_file = os.path.join(self.cfg.screenshots_dir, "k8s_diagnostics.log")
        os.makedirs(self.cfg.screenshots_dir, exist_ok=True)
        context = f"kind-{self.cfg.cluster_name}"
        with open(diag_file, "w", encoding="utf-8") as df:
            for ns in ["xmc", "dex"]:
                df.write(f"=== PODS (ns: {ns}) ===\n")
                pods_res = run_cmd(
                    ["kubectl", "--context", context, "get", "pods", "-n", ns, "-o", "wide"],
                    check=False,
                    capture_output=True,
                )
                df.write(pods_res.stdout + "\n" + pods_res.stderr + "\n")
                df.write(f"=== EVENTS (ns: {ns}) ===\n")
                events_res = run_cmd(
                    [
                        "kubectl",
                        "--context",
                        context,
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
                        "--context",
                        context,
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
                            "--context",
                            context,
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
            self._capture_diagnostics()
            raise

    def _resolve_crds_manifest(self) -> str:
        """Resolves the Bazel-assembled JobSet and Kueue CRD manifest, building if absent."""
        manifest_rel = os.path.join("test", "e2e", "manifests", "jobset_kueue_crds.yaml")
        default_path = os.path.join(self.cfg.workspace_root, "bazel-bin", manifest_rel)
        if os.path.isfile(default_path) and os.path.getsize(default_path) > 0:
            return default_path

        logger.info("Building //test/e2e/manifests:jobset_kueue_crds with Bazel...")
        run_cmd(
            ["bazel", "build", "//test/e2e/manifests:jobset_kueue_crds"],
            cwd=self.cfg.workspace_root,
        )
        if os.path.isfile(default_path) and os.path.getsize(default_path) > 0:
            return default_path

        bazel_bin_res = run_cmd(
            ["bazel", "info", "bazel-bin"],
            cwd=self.cfg.workspace_root,
            capture_output=True,
        )
        bazel_bin_lines = bazel_bin_res.stdout.strip().splitlines()
        bazel_bin = bazel_bin_lines[-1] if bazel_bin_lines else ""
        if not bazel_bin:
            raise RuntimeError("Failed to resolve bazel-bin directory via 'bazel info bazel-bin'")
        return os.path.join(bazel_bin, manifest_rel)

    def _apply_crds(self) -> None:
        """Applies upstream JobSet and Kueue CRDs with server-side apply and waits until established."""
        crds_manifest = self._resolve_crds_manifest()
        context = f"kind-{self.cfg.cluster_name}"
        logger.info("Applying upstream JobSet and Kueue CRDs from %s...", crds_manifest)
        run_cmd(
            [
                "kubectl",
                "--context",
                context,
                "apply",
                "--server-side",
                "--force-conflicts",
                "-f",
                crds_manifest,
            ]
        )
        run_cmd(
            [
                "kubectl",
                "--context",
                context,
                "wait",
                "--for=condition=established",
                "--timeout=60s",
                "-f",
                crds_manifest,
            ]
        )

    def deploy(self) -> None:
        """Copies chart, generates values, applies CRDs, and executes helm upgrade --install."""
        logger.info("=== [Helm] Deploying XMC Stack ===")
        self._prepare_helm_chart()
        self._patch_image_pull_policy()
        self._generate_values_file()
        self._apply_crds()

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
