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

"""Validation script for simple multi-unit job workload in XMC.

Asserts that the Simple Multi-Unit Job experiment and its associated WorkUnits
were correctly created, scheduled to the expected queue, configured with the
expected JobSet specification, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.simple_job_verify --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import xmc_entities_validator
from tools.nightly_tests.verification import verifier_cli, workload_verification

# Domain assertion constants for simple_job workload
EXPECTED_TITLE_PATTERN: str = "Demo Workload"
DEFAULT_WORK_UNITS: int = 3
DEFAULT_QUEUE: str = "cpu-queue"
EXPECTED_CPU_MILLICORES: int = 500
EXPECTED_MEMORY_BYTES: int = 512 * 1024 * 1024  # 512 MiB


def _assert_work_unit_spec(
    raw_wu: Any,
    worker_name: str,
    expected_queue: str,
) -> None:
    """Validates Priority, Scheduling Constraints, JobSet, Resources, and Metadata for a WorkUnit."""
    xmc_entities_validator.validate_work_unit_priority(raw_wu, expected_priority="HIGH")
    xmc_entities_validator.validate_work_unit_metadata(
        work_unit=raw_wu,
        expected_tags=["60s-workload", expected_queue, worker_name],
        expected_notes_substring="Demo workload",
    )
    xmc_entities_validator.validate_jobsets(
        work_unit=raw_wu,
        expected_jobs=[{"name": worker_name, "replicas": 1}],
    )
    job_spec = raw_wu.executable.jobset.jobs[0].spec
    xmc_entities_validator.validate_resource_requests(
        spec_or_job=job_spec,
        expected_cpu_milli=EXPECTED_CPU_MILLICORES,
        expected_memory_bytes=EXPECTED_MEMORY_BYTES,
    )
    if "multikueue" in expected_queue:
        meta = raw_wu.current_work_unit_execution.metadata
        mk = meta.multi_kueue_cluster_name
        ex = meta.executing_cluster.cluster_name
        mg = meta.management_cluster.cluster_name
        if not mk or ex != mk or not mg or ex == mg:
            raise AssertionError(
                f"WorkUnit '{raw_wu.name}' invalid MultiKueue metadata: {mk=}, {ex=}, {mg=}."
            )
    print(
        f"  [✓] Verified 100% Protobuf spec & resources for: {raw_wu.name} (Worker={worker_name})",
        flush=True,
    )


def _assert_all_work_unit_specs(stub: Any, xid: str) -> None:
    """Fetches raw work units and validates protobuf specifications for all units."""
    expected_work_units = verifier_cli.resolve_expected_work_units(DEFAULT_WORK_UNITS)
    expected_queue = verifier_cli.get_flag(verifier_cli.FLAGS, "expected_queue", DEFAULT_QUEUE)
    print("\n[Step 3] Validating 100% Protobuf specifications for each WorkUnit...", flush=True)
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if len(raw_work_units) != expected_work_units:
        raise AssertionError(
            f"Expected {expected_work_units} raw WorkUnits, but found {len(raw_work_units)}."
        )
    for raw_wu in raw_work_units:
        worker_name = xmc_entities_validator.extract_worker_name(raw_wu)
        _assert_work_unit_spec(raw_wu, worker_name, expected_queue)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for simple_job workload."""
    expected_work_units = verifier_cli.resolve_expected_work_units(DEFAULT_WORK_UNITS)
    expected_workers = (
        [f"demo-worker-{i}" for i in range(1, expected_work_units + 1)]
        if expected_work_units > 1
        else ["demo-worker"]
    )
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Simple Multi-Unit Job Verification & Assertions",
        summary_title="ALL SIMPLE JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_worker_names=expected_workers,
        extra_step=_assert_all_work_unit_specs,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
