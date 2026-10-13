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

import subprocess
from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import simple_job_verify, xmc_entities_validator
from tools.nightly_tests.verification import verifier_cli, workload_verification

DEFAULT_QUEUE: str = "multikueue-cpu-queue"
DEFAULT_WORKER_CLUSTER: str = "xmc-nightly-worker-1"
verifier_cli.flags.DEFINE_string(
    "expected_worker_cluster", DEFAULT_WORKER_CLUSTER, "Expected MultiKueue worker cluster."
)


def _run_kubectl(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["kubectl", *args], check=False, capture_output=True, text=True)


def _assert_wu_multikueue_metadata(stub: Any, wu: Any, expected_cluster: str) -> None:
    """Asserts MultiKueue execution metadata and cluster info on a single WorkUnit."""
    has_exec = getattr(wu, "HasField", lambda f: bool(getattr(wu, f, None)))(
        "current_work_unit_execution"
    )
    req = xmc_entities_validator._make_req("GetCurrentWorkUnitExecutionRequest", name=wu.name)
    exec_pb = wu.current_work_unit_execution if has_exec else stub.GetCurrentWorkUnitExecution(req)
    meta = getattr(exec_pb, "metadata", None)
    mk = getattr(meta, "multi_kueue_cluster_name", "")
    ex, mg = getattr(meta, "executing_cluster", None), getattr(meta, "management_cluster", None)
    ex_name, mg_name = getattr(ex, "cluster_name", ""), getattr(mg, "cluster_name", "")
    if (
        mk != expected_cluster
        or ex_name != expected_cluster
        or not (getattr(ex, "project", "") and getattr(ex, "location", ""))
        or not (mg_name and getattr(mg, "project", "") and getattr(mg, "location", ""))
        or ex_name == mg_name
    ):
        raise AssertionError(f"WorkUnit '{wu.name}' invalid MultiKueue metadata: {meta}")


def _verify_k8s_multikueue_crs(xid: str, queue: str, cluster: str, count: int) -> None:
    """Verifies MultiKueueCluster, AdmissionCheck, and Workload.status.clusterName via kubectl."""
    jp = 'jsonpath={.status.conditions[?(@.type=="Active")].status}'
    for kind, name in (
        ("multikueuecluster", cluster),
        ("admissioncheck", "multikueue-admission-check"),
    ):
        res = _run_kubectl("get", kind, name, "-o", jp)
        if res.returncode != 0 or res.stdout.strip() != "True":
            raise AssertionError(f"{kind}/{name} condition Active=True failed: {res.stderr}")
    wl_jp = (
        f"jsonpath={{.items[?(@.spec.podSets[0].template.metadata.labels"
        f'.xmc\\.google\\.com/experiment-id=="{xid}")].status.clusterName}}'
    )
    wl = _run_kubectl("get", "workloads", "-n", queue, "-o", wl_jp)
    clusters = wl.stdout.split() if wl.returncode == 0 else []
    if wl.returncode != 0 or len(clusters) != count or set(clusters) != {cluster}:
        raise AssertionError(
            f"Workload.status.clusterName={clusters} (expected {count}x {cluster!r})"
        )


def _assert_multikueue_workload(stub: Any, xid: str) -> None:
    """Runs standard simple_job spec checks plus MultiKueue metadata and K8s CR checks."""
    simple_job_verify._assert_all_work_unit_specs(stub, xid)
    f = verifier_cli.FLAGS
    queue = verifier_cli.get_flag(f, "expected_queue", DEFAULT_QUEUE)
    cluster = verifier_cli.get_flag(f, "expected_worker_cluster", DEFAULT_WORKER_CLUSTER)
    count = verifier_cli.resolve_expected_work_units(simple_job_verify.DEFAULT_WORK_UNITS)
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    for wu in getattr(stub.ListWorkUnits(req), "work_units", []):
        _assert_wu_multikueue_metadata(stub, wu, cluster)
    _verify_k8s_multikueue_crs(xid, queue, cluster, count)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for multikueue_job workload."""
    f: Any = verifier_cli.FLAGS
    if "expected_queue" in f and not f["expected_queue"].present:
        f.expected_queue = DEFAULT_QUEUE
    count = verifier_cli.resolve_expected_work_units(simple_job_verify.DEFAULT_WORK_UNITS)
    workers = [f"demo-worker-{i}" for i in range(1, count + 1)] if count > 1 else ["demo-worker"]
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC MultiKueue Delegated Job Verification & Assertions",
        summary_title="ALL MULTIKUEUE JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=simple_job_verify.EXPECTED_TITLE_PATTERN,
        expected_worker_names=workers,
        extra_step=_assert_multikueue_workload,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
