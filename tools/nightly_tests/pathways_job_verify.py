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

"""Validation script for Pathways multi-component JobSet workload in XMC.

Asserts that the Pathways validation experiment and its WorkUnit were correctly
created, scheduled to the expected queue (e.g. tpu-queue or cpu-queue), configured
with pathways-head (init containers with container port mappings) and worker daemon roles,
and executed to completion.

Usage:
    python3 -m tools.nightly_tests.pathways_job_verify --xid=26 --expected_queue=tpu-queue
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

EXPECTED_TITLE_PATTERN: str = "Pathways Multi-Component JobSet Validation"
EXPECTED_WORK_UNITS: int = 1
DEFAULT_QUEUE: str = "tpu-queue"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("pathways-head",)
DEFAULT_TIMEOUT_SEC: int = 600

EXPECTED_JOBSET_SPECS = [
    {
        "name": "pathways-head",
        "replicas": 1,
        "init_containers": ["pathways-rm", "pathways-proxy"],
        "primary_container": "jax-tpu",
    },
    {
        "name": "worker",
        "replicas": 2,
        "primary_container": "pathways-worker",
    },
]


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


def _assert_work_units(
    stub: Any, xid: str, expected_queue: str, timeout_sec: int
) -> list[dict[str, Any]]:
    """Asserts work unit execution state and queue mapping."""
    print(f"\n[2/3] Asserting {EXPECTED_WORK_UNITS} WorkUnit targeted at '{expected_queue}'...")
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=EXPECTED_WORK_UNITS,
        expected_queue=expected_queue,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        timeout_sec=timeout_sec,
    )
    for wu in wu_results:
        print(
            f"  [✓] WorkUnit #{wu['id']}: Worker='{wu['worker_name']}', Queue='{wu['queue']}', State='{wu['state']}'"
        )
    return wu_results


def _assert_jobset_topology(stub: Any, xid: str) -> list[dict[str, Any]]:
    """Validates Pathways JobSet specifications."""
    print("\n[3/3] Validating Pathways JobSet specifications...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if not raw_work_units:
        raise AssertionError(f"No WorkUnits found for experiment {xid}.")
    job_results = xmc_entities_validator.validate_jobsets(
        work_unit=raw_work_units[0],
        expected_jobs=EXPECTED_JOBSET_SPECS,
    )
    for job_info in job_results:
        print(
            f"  [✓] Job '{job_info['name']}': Replicas={job_info['replicas']}, "
            f"InitContainers={[c['name'] for c in job_info['init_containers']]}, "
            f"PrimaryContainer='{job_info['primary_container']}'"
        )
    return job_results


def _print_banner(xid: str, expected_queue: str, timeout_sec: int) -> None:
    """Prints verification header banner."""
    common.print_banner(
        "XMC Pathways Multi-Component Job Verification",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", expected_queue),
            ("Expected Primary Worker", list(EXPECTED_WORKER_NAMES)),
            ("Expected JobSet Roles", [j["name"] for j in EXPECTED_JOBSET_SPECS]),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_summary(
    experiment: Any,
    wu_count: int,
    expected_queue: str,
    job_results: list[dict[str, Any]],
    ui_base_url: str,
    xid: str,
) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL PATHWAYS JOB ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
            ("Verified WorkUnits", f"{wu_count} / {EXPECTED_WORK_UNITS}"),
            ("Target Queue", expected_queue),
            ("Verified Roles", [j["name"] for j in job_results]),
            ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for pathways_job workload."""
    common.validate_argv(argv)
    xid = common.parse_xid(getattr(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()

    _print_banner(xid, expected_queue, timeout_sec)
    with (
        common.handle_verification_errors(),
        common.ess_channel_context(getattr(FLAGS, "endpoint", None)) as (stub, _),
    ):
        experiment = _assert_experiment(stub, xid)
        wu_results = _assert_work_units(stub, xid, expected_queue, timeout_sec)
        job_results = _assert_jobset_topology(stub, xid)
        _print_summary(experiment, len(wu_results), expected_queue, job_results, ui_base_url, xid)


if __name__ == "__main__":
    common.run_main(main)
