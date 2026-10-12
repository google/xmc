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
"""Validation script for MultiKueue delegated workload in XMC."""

from __future__ import annotations

import os
import subprocess
from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import xmc_entities_validator
from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "MultiKueue Workload"
DEFAULT_WORK_UNITS: int = 2
DEFAULT_QUEUE: str = "multikueue-cpu-queue"
DEFAULT_WORKER_CLUSTER: str = "xmc-nightly-worker-1"
verifier_cli.flags.DEFINE_string(
    "expected_worker_cluster", DEFAULT_WORKER_CLUSTER, "Expected worker cluster."
)


def _verify_k8s_multikueue_crs(
    xid: str, queue: str = DEFAULT_QUEUE, worker_cluster: str = DEFAULT_WORKER_CLUSTER
) -> None:
    """Verifies MultiKueueCluster, AdmissionCheck, and Workload.status.clusterName via kubectl."""
    kw: dict[str, Any] = {"capture_output": True, "text": True}
    try:
        ctx = subprocess.run(["kubectl", "config", "current-context"], check=False, **kw)
    except FileNotFoundError:
        return
    if ctx.returncode != 0 or not ctx.stdout.strip():
        return
    jp = 'jsonpath={.status.conditions[?(@.type=="Active")].status}'
    for kind, name in (
        ("multikueuecluster", worker_cluster),
        ("admissioncheck", "multikueue-admission-check"),
    ):
        res = subprocess.run(["kubectl", "get", kind, name, "-o", jp], check=False, **kw)
        if res.returncode != 0 or res.stdout.strip() != "True":
            raise AssertionError(
                f"{kind}/{name} condition Active=True failed "
                f"(stdout={res.stdout.strip()!r}, stderr={res.stderr.strip()!r})"
            )
    wl_jp = (
        f"jsonpath={{.items[?(@.spec.podSets[0].template.metadata.labels"
        f'.xmc\\.google\\.com/experiment-id=="{xid}")].status.clusterName}}'
    )
    wl = subprocess.run(
        ["kubectl", "get", "workloads", "-n", queue, "-o", wl_jp], check=False, **kw
    )
    clusters = wl.stdout.split() if wl.returncode == 0 else []
    if wl.returncode != 0 or not clusters or set(clusters) != {worker_cluster}:
        raise AssertionError(
            f"Workload.status.clusterName={clusters} != {worker_cluster} "
            f"(rc={wl.returncode}, stderr={wl.stderr.strip()!r})"
        )


def _assert_multikueue_work_units(stub: Any, xid: str) -> None:
    """Validates MultiKueue execution metadata and K8s MultiKueue CRs."""
    f = verifier_cli.FLAGS
    q = verifier_cli.get_flag(f, "expected_queue", DEFAULT_QUEUE)
    c = verifier_cli.get_flag(f, "expected_worker_cluster", DEFAULT_WORKER_CLUSTER)
    xmc_entities_validator.validate_multikueue_execution_metadata(stub, xid, c)
    _verify_k8s_multikueue_crs(xid, q, c)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for multikueue_job workload."""
    f: Any = verifier_cli.FLAGS
    if getattr(f, "expected_queue", None) == "cpu-queue" and "QUEUE_NAME" not in os.environ:
        f.expected_queue = DEFAULT_QUEUE
    count = verifier_cli.resolve_expected_work_units(DEFAULT_WORK_UNITS)
    workers = [f"multikueue-worker-{i}" for i in range(1, count + 1)]
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC MultiKueue Delegated Job Verification & Assertions",
        summary_title="ALL MULTIKUEUE JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=[{"name": workers[0], "replicas": 1, "primary_container": "primary-worker"}],
        expected_worker_names=workers,
        extra_step=_assert_multikueue_work_units,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
