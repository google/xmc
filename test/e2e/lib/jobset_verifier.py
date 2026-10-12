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

"""Verifies JobSet custom resources and work unit scheduling state in KinD E2E tests."""

from __future__ import annotations

import json
import logging
import os
import subprocess
import time
from typing import Any

DEFAULT_KUBE_CONTEXT = "kind-xmc-e2e"
DEFAULT_QUEUE_NAME = "cpu-queue"
DEFAULT_POLL_TIMEOUT_SECONDS = 30.0
DEFAULT_POLL_INTERVAL_SECONDS = 1.0
KUBECTL_TIMEOUT_SECONDS = 10.0

logger = logging.getLogger(__name__)


def build_work_unit_payload(queue_name: str = DEFAULT_QUEUE_NAME) -> dict[str, Any]:
    """Builds a minimal valid CreateWorkUnit payload targeting the given queue."""
    return {
        "priority": "PRIORITY_MEDIUM",
        "workUnitType": "STANDARD",
        "executable": {
            "schedulingConstraints": {
                "queueName": queue_name,
            },
            "jobset": {
                "jobs": [
                    {
                        "name": "worker",
                        "replicas": 1,
                        "spec": {
                            "dockerImage": "busybox:1.36",
                            "command": ["sh", "-c", "echo hello"],
                            "resources": {
                                "cpuResources": {
                                    "cpuMillicores": 500,
                                    "memoryBytes": 536870912,
                                },
                            },
                        },
                    },
                ],
            },
        },
    }


def _extract_experiment_id(experiment_name: str) -> str:
    """Extracts the numeric experiment ID from an experiment resource name."""
    if not experiment_name or not isinstance(experiment_name, str):
        raise ValueError("Experiment name cannot be None or empty")
    prefix = "experiments/"
    if not experiment_name.startswith(prefix) or len(experiment_name) <= len(prefix):
        raise ValueError(f"Invalid experiment resource name: {experiment_name!r}")
    return experiment_name[len(prefix) :]


def _fetch_jobsets_for_experiment(
    experiment_id: str,
    namespace: str,
    kube_context: str,
) -> list[dict[str, Any]]:
    """Queries Kubernetes for JobSet resources matching the experiment ID label."""
    result = subprocess.run(
        [
            "kubectl",
            "--context",
            kube_context,
            "get",
            "jobsets.jobset.x-k8s.io",
            "-n",
            namespace,
            "-l",
            f"xmc.google.com/experiment-id={experiment_id}",
            "-o",
            "json",
        ],
        capture_output=True,
        text=True,
        check=True,
        timeout=KUBECTL_TIMEOUT_SECONDS,
    )
    payload = json.loads(result.stdout)
    return list(payload.get("items") or [])


def _log_actuator_diagnostics(kube_context: str) -> None:
    """Logs recent actuator container output to aid debugging on JobSet poll timeout."""
    try:
        result = subprocess.run(
            [
                "kubectl",
                "--context",
                kube_context,
                "logs",
                "-n",
                "xmc",
                "deployment/actuator-deployment",
                "--all-containers=true",
                "--tail=40",
            ],
            capture_output=True,
            text=True,
            check=False,
            timeout=KUBECTL_TIMEOUT_SECONDS,
        )
    except subprocess.SubprocessError as exc:
        logger.warning("Failed to fetch actuator diagnostic logs: %s", exc)
        return
    output = (result.stdout or result.stderr or "").strip()
    if output:
        logger.error("Actuator logs before timeout:\n%s", output)


def wait_for_jobset_for_experiment(
    experiment_name: str,
    namespace: str = DEFAULT_QUEUE_NAME,
    kube_context: str | None = None,
    timeout_seconds: float = DEFAULT_POLL_TIMEOUT_SECONDS,
    poll_interval_seconds: float = DEFAULT_POLL_INTERVAL_SECONDS,
) -> dict[str, Any]:
    """Polls Kubernetes until the actuator creates a JobSet for the experiment."""
    experiment_id = _extract_experiment_id(experiment_name)
    resolved_context = kube_context or os.environ.get("KUBE_CONTEXT", DEFAULT_KUBE_CONTEXT)
    deadline = time.monotonic() + timeout_seconds
    while True:
        try:
            items = _fetch_jobsets_for_experiment(experiment_id, namespace, resolved_context)
            if items:
                return items[0]
        except subprocess.SubprocessError as exc:
            stderr = getattr(exc, "stderr", None)
            logger.warning(
                "Transient error fetching JobSets (will retry): %s (stderr: %s)",
                exc,
                stderr,
            )
        except json.JSONDecodeError as exc:
            logger.warning("Transient error decoding JobSets JSON (will retry): %s", exc)
        if time.monotonic() >= deadline:
            _log_actuator_diagnostics(resolved_context)
            raise RuntimeError(
                f"Timed out after {timeout_seconds}s waiting for JobSet in namespace "
                f"{namespace!r} for {experiment_name}"
            )
        time.sleep(poll_interval_seconds)


def assert_jobset_contract(
    jobset: dict[str, Any],
    expected_queue: str = DEFAULT_QUEUE_NAME,
) -> None:
    """Asserts XMC's Non-Restarting JobSets Contract and Kueue queue labels on a JobSet."""
    if not jobset:
        raise ValueError("JobSet dictionary cannot be None or empty")
    metadata = jobset.get("metadata") or {}
    labels = metadata.get("labels") or {}
    if labels.get("kueue.x-k8s.io/queue-name") != expected_queue:
        raise RuntimeError(f"Expected kueue.x-k8s.io/queue-name={expected_queue!r}, got {labels!r}")
    if labels.get("app.kubernetes.io/managed-by") != "xmc":
        raise RuntimeError(f"Expected app.kubernetes.io/managed-by='xmc', got {labels!r}")

    spec = jobset.get("spec") or {}
    failure_policy = spec.get("failurePolicy") or {}
    max_restarts = failure_policy.get("maxRestarts", 0)
    if max_restarts != 0:
        raise RuntimeError(f"Expected spec.failurePolicy.maxRestarts == 0, got {max_restarts!r}")

    replicated_jobs = spec.get("replicatedJobs") or []
    if not replicated_jobs:
        raise RuntimeError("Expected non-empty spec.replicatedJobs on JobSet")
    for rj in replicated_jobs:
        template = (rj or {}).get("template") or {}
        job_spec = template.get("spec") or {}
        pod_template = job_spec.get("template") or {}
        pod_spec = pod_template.get("spec") or {}
        restart_policy = pod_spec.get("restartPolicy")
        if restart_policy != "Never":
            raise RuntimeError(
                f"Expected pod spec restartPolicy == 'Never', got {restart_policy!r}"
            )
