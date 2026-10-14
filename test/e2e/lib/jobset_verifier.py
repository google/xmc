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
from collections.abc import Sequence
from typing import Any

from test.e2e.lib.polling import PollTimeoutError, poll_until

DEFAULT_KUBE_CONTEXT = "kind-xmc-e2e"
DEFAULT_QUEUE_NAME = "cpu-queue"
DEFAULT_POLL_TIMEOUT_SECONDS = 30.0
DEFAULT_POLL_INTERVAL_SECONDS = 1.0
KUBECTL_TIMEOUT_SECONDS = 10.0
# Served by registry.k8s.io, which does not rate-limit pulls; the actuator always pulls images.
E2E_IMAGE = "registry.k8s.io/e2e-test-images/busybox:1.29-4"
SIMPLE_JOB_COMMAND = ("sh", "-c", "echo hello")

logger = logging.getLogger(__name__)


def build_work_unit_payload(
    queue_name: str = DEFAULT_QUEUE_NAME,
    command: Sequence[str] = SIMPLE_JOB_COMMAND,
) -> dict[str, Any]:
    """Builds a minimal valid CreateWorkUnit payload running command on the given queue."""
    return {
        "priority": "NORMAL",
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
                            "dockerImage": E2E_IMAGE,
                            "command": list(command),
                            "resources": {
                                "cpuResources": {
                                    "cpuMillicores": 100,
                                    "memoryBytes": 134217728,
                                },
                            },
                        },
                    },
                ],
            },
        },
    }


def _work_unit_selector(work_unit_name: str) -> str:
    """Returns the label selector of the JobSets that the actuator creates for a work unit."""
    parts = work_unit_name.split("/") if isinstance(work_unit_name, str) else []
    if len(parts) != 4 or parts[0] != "experiments" or parts[2] != "workUnits" or not all(parts):
        raise ValueError(f"Invalid work unit resource name: {work_unit_name!r}")
    return f"xmc.google.com/experiment-id={parts[1]},xmc.google.com/work-unit-id={parts[3]}"


def _list_resources(
    resource: str,
    namespace: str,
    kube_context: str,
    selector: str = "",
) -> list[dict[str, Any]]:
    """Lists the resources of a kind in a namespace that match a label selector."""
    result = subprocess.run(
        [
            "kubectl",
            "--context",
            kube_context,
            "get",
            resource,
            "-n",
            namespace,
            "-l",
            selector,
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


def wait_for_jobset_for_work_unit(
    work_unit_name: str,
    namespace: str = DEFAULT_QUEUE_NAME,
    kube_context: str | None = None,
    timeout_seconds: float = DEFAULT_POLL_TIMEOUT_SECONDS,
    poll_interval_seconds: float = DEFAULT_POLL_INTERVAL_SECONDS,
) -> dict[str, Any]:
    """Polls Kubernetes until the actuator creates a JobSet for the work unit.

    Args:
        work_unit_name: Work unit resource name, such as experiments/1/workUnits/2.
        namespace: Namespace of the work unit's queue.
        kube_context: kubectl context; defaults to $KUBE_CONTEXT, else the KinD E2E context.
        timeout_seconds: Seconds after which waiting gives up.
        poll_interval_seconds: Seconds between queries.

    Returns:
        The JobSet of the work unit.

    Raises:
        ValueError: If work_unit_name is not a work unit resource name.
        PollTimeoutError: If no JobSet appears before the deadline.
    """
    selector = _work_unit_selector(work_unit_name)
    resolved_context = _resolve_kube_context(kube_context)

    def probe() -> dict[str, Any] | None:
        items = _list_resources("jobsets.jobset.x-k8s.io", namespace, resolved_context, selector)
        return items[0] if items else None

    description = f"JobSet in namespace {namespace!r} for {work_unit_name}"
    try:
        return poll_until(probe, description, timeout_seconds, poll_interval_seconds)
    except PollTimeoutError:
        _log_actuator_diagnostics(resolved_context)
        raise


def wait_for_workload_admitted(
    jobset: dict[str, Any],
    kube_context: str | None = None,
    timeout_seconds: float = DEFAULT_POLL_TIMEOUT_SECONDS,
    poll_interval_seconds: float = DEFAULT_POLL_INTERVAL_SECONDS,
) -> dict[str, Any]:
    """Polls until Kueue admits the Workload that it created for a JobSet.

    Kueue keeps the Admitted condition after the Workload finishes, so this also holds for
    jobs that complete before the first poll.

    Args:
        jobset: The JobSet, as returned by wait_for_jobset_for_work_unit.
        kube_context: kubectl context; defaults to $KUBE_CONTEXT, else the KinD E2E context.
        timeout_seconds: Seconds after which waiting gives up.
        poll_interval_seconds: Seconds between queries.

    Returns:
        The admitted Workload.

    Raises:
        ValueError: If the JobSet has no namespace or UID.
        PollTimeoutError: If the Workload is not admitted before the deadline.
    """
    metadata = jobset.get("metadata") or {}
    namespace, owner_uid = metadata.get("namespace"), metadata.get("uid")
    if not namespace or not owner_uid:
        raise ValueError(f"JobSet metadata has no namespace or UID: {metadata!r}")
    resolved_context = _resolve_kube_context(kube_context)

    def probe() -> dict[str, Any] | None:
        workloads = _list_resources("workloads.kueue.x-k8s.io", namespace, resolved_context)
        workload = _find_owned_workload(workloads, owner_uid)
        return workload if workload and _has_condition(workload, "Admitted") else None

    description = f"admitted Workload of JobSet {metadata.get('name')}"
    return poll_until(probe, description, timeout_seconds, poll_interval_seconds)


def _resolve_kube_context(kube_context: str | None) -> str:
    """Returns kube_context, else $KUBE_CONTEXT, else the default KinD E2E context."""
    return kube_context or os.environ.get("KUBE_CONTEXT", DEFAULT_KUBE_CONTEXT)


def _find_owned_workload(workloads: list[dict[str, Any]], owner_uid: str) -> dict[str, Any] | None:
    """Returns the Workload with an owner reference to owner_uid, if any."""
    for workload in workloads:
        owners = (workload.get("metadata") or {}).get("ownerReferences") or []
        if any(owner.get("uid") == owner_uid for owner in owners):
            return workload
    return None


def _has_condition(resource: dict[str, Any], condition_type: str) -> bool:
    """Returns whether a resource has a status condition of the given type set to True."""
    conditions = (resource.get("status") or {}).get("conditions") or []
    return any(c.get("type") == condition_type and c.get("status") == "True" for c in conditions)


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
