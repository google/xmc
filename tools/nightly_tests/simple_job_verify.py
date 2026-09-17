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

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

# Domain assertion constants for simple_job workload
EXPECTED_TITLE_PATTERN: str = "Demo Workload"
DEFAULT_WORK_UNITS: int = 3
DEFAULT_QUEUE: str = "cpu-queue"
DEFAULT_TIMEOUT_SEC: int = 600
EXPECTED_CPU_MILLICORES: int = 500
EXPECTED_MEMORY_BYTES: int = 512 * 1024 * 1024  # 512 MiB


def _assert_experiment(stub: Any, xid: str) -> Any:
    """Asserts experiment existence and title pattern."""
    print(f"\n[1/3] Asserting Experiment metadata for XID {xid}...")
    experiment = xmc_entities_validator.validate_experiment(
        stub=stub,
        xid=xid,
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
    )
    print(f"  [✓] Experiment Name   : {experiment.name}")
    print(f"  [✓] Experiment Title  : {experiment.title}")
    return experiment


def _assert_work_unit_states(
    stub: Any,
    xid: str,
    expected_queue: str,
    expected_workers: list[str],
    expected_work_units: int,
    timeout_sec: int,
) -> list[dict[str, Any]]:
    """Asserts work units completion, queues, and worker names."""
    print(f"\n[2/3] Asserting {expected_work_units} WorkUnits targeted at '{expected_queue}'...")
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=expected_work_units,
        expected_queue=expected_queue,
        expected_worker_names=expected_workers,
        timeout_sec=timeout_sec,
    )
    for wu_info in wu_results:
        print(
            f"  [✓] WorkUnit #{wu_info['id']}: Worker='{wu_info['worker_name']}', "
            f"Queue='{wu_info['queue']}', State='{wu_info['state']}'"
        )
    return wu_results


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
    print(
        f"  [✓] Verified 100% Protobuf spec & resources for: {raw_wu.name} (Worker={worker_name})"
    )


def _assert_all_work_unit_specs(
    stub: Any,
    xid: str,
    expected_work_units: int,
    expected_queue: str,
) -> None:
    """Fetches raw work units and validates protobuf specifications for all units."""
    print("\n[3/3] Validating 100% Protobuf specifications for each WorkUnit...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if len(raw_work_units) != expected_work_units:
        raise AssertionError(
            f"Expected {expected_work_units} raw WorkUnits, but found {len(raw_work_units)}."
        )
    for raw_wu in raw_work_units:
        worker_name = xmc_entities_validator.extract_worker_name(raw_wu)
        _assert_work_unit_spec(raw_wu, worker_name, expected_queue)


def _print_simple_job_banner(
    xid: str,
    expected_queue: str,
    expected_work_units: int,
    expected_workers: list[str],
    timeout_sec: int,
) -> None:
    """Prints verification header banner."""
    common.print_banner(
        "XMC Simple Multi-Unit Job Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", expected_work_units),
            ("Expected Target Queue", expected_queue),
            ("Expected Worker Names", expected_workers),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_simple_job_summary(
    experiment: Any,
    wu_count: int,
    expected_work_units: int,
    expected_queue: str,
    ui_base_url: str,
    xid: str,
) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL SIMPLE JOB ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
            ("Verified WorkUnits", f"{wu_count} / {expected_work_units}"),
            ("Target Queue", expected_queue),
            ("Priority Asserted", "HIGH (3)"),
            ("CPU Requested", f"{EXPECTED_CPU_MILLICORES}m"),
            ("Memory Requested", f"{EXPECTED_MEMORY_BYTES // (1024 * 1024)} MiB"),
            ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for simple_job workload."""
    common.validate_argv(argv)
    xid = common.parse_xid(getattr(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    expected_work_units = common.get_flag(FLAGS, "expected_work_units", DEFAULT_WORK_UNITS)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()
    expected_workers = (
        [f"demo-worker-{i}" for i in range(1, expected_work_units + 1)]
        if expected_work_units > 1
        else ["demo-worker"]
    )
    _print_simple_job_banner(
        xid, expected_queue, expected_work_units, expected_workers, timeout_sec
    )
    with (
        common.handle_verification_errors(),
        common.ess_channel_context(getattr(FLAGS, "endpoint", None)) as (stub, _),
    ):
        experiment = _assert_experiment(stub, xid)
        wu_results = _assert_work_unit_states(
            stub, xid, expected_queue, expected_workers, expected_work_units, timeout_sec
        )
        _assert_all_work_unit_specs(stub, xid, expected_work_units, expected_queue)
        _print_simple_job_summary(
            experiment, len(wu_results), expected_work_units, expected_queue, ui_base_url, xid
        )


if __name__ == "__main__":
    common.run_main(main)
