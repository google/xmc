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

"""Kubernetes cluster diagnostic telemetry collector for local dev troubleshooting."""

from __future__ import annotations

import logging
import shutil
from pathlib import Path

from tools.common.tool_utils import log_error, log_info, run_command

_LOGGER = logging.getLogger("xmc.dev_env.cluster_diagnostics")


class ClusterDiagnostics:
    """Collects pod descriptions, cluster events, and container logs on rollout failure."""

    def __init__(self, kube_context: str, output_dir: Path | None = None) -> None:
        """Initializes ClusterDiagnostics with kubectl context and output directory."""
        self.kube_context = kube_context
        self.output_dir = output_dir

    def _capture_pods(self, ctx: str, ns: str) -> list[str]:
        """Captures wide pod listing in a given namespace."""
        lines = [f"=== PODS (namespace: {ns}) ==="]
        res = run_command(
            [
                "kubectl",
                "--context",
                ctx,
                "--request-timeout=10s",
                "get",
                "pods",
                "-n",
                ns,
                "-o",
                "wide",
            ],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        lines.append(res.stdout or res.stderr or "")
        return lines

    def _capture_pod_descriptions(self, ctx: str, ns: str) -> list[str]:
        """Captures detailed pod descriptions in a given namespace."""
        lines = [f"=== POD DESCRIPTIONS (namespace: {ns}) ==="]
        desc_res = run_command(
            ["kubectl", "--context", ctx, "--request-timeout=10s", "describe", "pods", "-n", ns],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        lines.append(desc_res.stdout or desc_res.stderr or "")
        return lines

    def _capture_events(self, ctx: str, ns: str) -> list[str]:
        """Captures recent Kubernetes events in a given namespace."""
        lines = [f"=== EVENTS (namespace: {ns}) ==="]
        ev_res = run_command(
            [
                "kubectl",
                "--context",
                ctx,
                "--request-timeout=10s",
                "get",
                "events",
                "-n",
                ns,
                "--sort-by=.metadata.creationTimestamp",
            ],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        lines.append(ev_res.stdout or ev_res.stderr or "")
        return lines

    def _capture_pod_logs(self, ctx: str, ns: str) -> list[str]:
        """Captures tail logs for all containers in each pod in a namespace."""
        lines: list[str] = []
        pods_res = run_command(
            [
                "kubectl",
                "--context",
                ctx,
                "--request-timeout=10s",
                "get",
                "pods",
                "-n",
                ns,
                "-o",
                "jsonpath={range .items[*]}{.metadata.name}{'\\t'}{.status.phase}{'\\t'}{range .status.conditions[?(@.type=='Ready')]}{.status}{end}{'\\t'}{range .status.containerStatuses[*]}{.restartCount}{end}{'\\n'}{end}",
            ],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        if pods_res.returncode == 0:
            unhealthy_pods: list[str] = []
            other_pods: list[str] = []
            for line in (pods_res.stdout or "").splitlines():
                parts = line.strip().split("\t")
                if not parts or not parts[0]:
                    continue
                pod_name = parts[0].strip()
                phase = parts[1].strip() if len(parts) > 1 else ""
                ready = parts[2].strip() if len(parts) > 2 else ""
                restarts = parts[3].strip() if len(parts) > 3 else "0"
                has_restarts = any(c.isdigit() and c != "0" for c in restarts)
                if phase != "Running" or ready != "True" or has_restarts:
                    unhealthy_pods.append(pod_name)
                else:
                    other_pods.append(pod_name)

            if not unhealthy_pods and not other_pods and pods_res.stdout:
                target_pods = pods_res.stdout.split()[:15]
            else:
                target_pods = (unhealthy_pods + other_pods)[:10]

            for pod in target_pods:
                lines.append(f"--- Logs for pod: {pod} (ns: {ns}) ---")
                log_res = run_command(
                    [
                        "kubectl",
                        "--context",
                        ctx,
                        "--request-timeout=10s",
                        "logs",
                        "-n",
                        ns,
                        pod,
                        "--all-containers=true",
                        "--tail=100",
                    ],
                    capture_output=True,
                    check=False,
                    log_cmd=False,
                )
                lines.append(log_res.stdout or log_res.stderr or "")
        return lines

    def capture_diagnostics(
        self,
        namespace: str = "xmc",
        output_file: Path | None = None,
    ) -> Path:
        """Captures pod descriptions, cluster events, and container logs on rollout failure.

        Saves detailed cluster diagnostics across 'dex' and the target XMC namespace to
        `k8s_diagnostics.log` for troubleshooting failed deployments.

        Args:
            namespace: Target Kubernetes namespace containing XMC deployments (default: 'xmc').
            output_file: Optional Path to target diagnostic file; generated if omitted.

        Returns:
            Path to the generated diagnostic log file.
        """
        log_error("Rollout failure detected. Capturing diagnostic telemetry...")
        if output_file:
            diag_file = output_file
        elif self.output_dir:
            diag_file = self.output_dir / "k8s_diagnostics.log"
        else:
            diag_file = Path("/tmp/k8s_diagnostics.log")

        diag_file.parent.mkdir(parents=True, exist_ok=True)

        if not shutil.which("kubectl"):
            log_error("kubectl CLI not found. Skipping diagnostic capture.")
            diag_file.write_text(
                "kubectl CLI not found. Diagnostics could not be captured.", encoding="utf-8"
            )
            return diag_file

        ctx = self.kube_context
        # Verify cluster reachability first to avoid cascading timeouts
        reachability = run_command(
            ["kubectl", "--context", ctx, "--request-timeout=3s", "get", "nodes"],
            capture_output=True,
            check=False,
            log_cmd=False,
        )
        if reachability.returncode != 0:
            log_error("Kubernetes cluster is unreachable. Skipping detailed diagnostics.")
            diag_file.write_text(
                f"Cluster unreachable: {reachability.stderr or reachability.stdout or 'connection failed'}",
                encoding="utf-8",
            )
            return diag_file

        lines: list[str] = []
        namespaces = (namespace, "dex") if namespace != "dex" else ("dex",)

        for ns in namespaces:
            lines.extend(self._capture_pods(ctx, ns))
            lines.extend(self._capture_pod_descriptions(ctx, ns))
            lines.extend(self._capture_events(ctx, ns))
            lines.extend(self._capture_pod_logs(ctx, ns))

        diag_file.write_text("\n".join(lines), encoding="utf-8")
        log_info(f"Diagnostic logs saved to: '{diag_file}'")
        return diag_file
