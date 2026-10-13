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
ADMISSION_CHECK_NAME: str = "multikueue-admission-check"
ACTIVE_CONDITION_JSONPATH: str = 'jsonpath={.status.conditions[?(@.type=="Active")].status}'
verifier_cli.flags.DEFINE_string(
    "expected_worker_cluster", DEFAULT_WORKER_CLUSTER, "Expected MultiKueue worker cluster."
)


def _run_kubectl(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["kubectl", *args], check=False, capture_output=True, text=True)


def _assert_k8s_resource_active(resource_kind: str, resource_name: str) -> None:
    result = _run_kubectl("get", resource_kind, resource_name, "-o", ACTIVE_CONDITION_JSONPATH)
    if result.returncode != 0 or result.stdout.strip() != "True":
        raise AssertionError(f"{resource_kind}/{resource_name} Active=True failed: {result.stderr}")


def _assert_k8s_workloads_assigned_cluster(
    xid: str, queue: str, cluster: str, expected_count: int
) -> None:
    label_path = r"@.spec.podSets[0].template.metadata.labels.xmc\.google\.com/experiment-id"
    jsonpath = f'jsonpath={{.items[?({label_path}=="{xid}")].status.clusterName}}'
    result = _run_kubectl("get", "workloads", "-n", queue, "-o", jsonpath)
    clusters = result.stdout.split() if result.returncode == 0 else []
    if result.returncode != 0 or len(clusters) != expected_count or set(clusters) != {cluster}:
        raise AssertionError(
            f"Workload.status.clusterName={clusters} (expected {expected_count}x {cluster!r})"
        )


def _assert_multikueue_workload(stub: Any, xid: str) -> None:
    simple_job_verify.assert_all_work_unit_specs(stub, xid)
    cli_flags = verifier_cli.FLAGS
    queue_name = verifier_cli.get_flag(cli_flags, "expected_queue", DEFAULT_QUEUE)
    cluster = verifier_cli.get_flag(cli_flags, "expected_worker_cluster", DEFAULT_WORKER_CLUSTER)
    unit_count = verifier_cli.resolve_expected_work_units(simple_job_verify.DEFAULT_WORK_UNITS)
    xmc_entities_validator.validate_multikueue_execution_metadata(stub, xid, cluster)
    _assert_k8s_resource_active("multikueuecluster", cluster)
    _assert_k8s_resource_active("admissioncheck", ADMISSION_CHECK_NAME)
    _assert_k8s_workloads_assigned_cluster(xid, queue_name, cluster, unit_count)


def main(argv: Sequence[str]) -> None:
    cli_flags: Any = verifier_cli.FLAGS
    if "expected_queue" in cli_flags and not cli_flags["expected_queue"].present:
        cli_flags.expected_queue = DEFAULT_QUEUE
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
