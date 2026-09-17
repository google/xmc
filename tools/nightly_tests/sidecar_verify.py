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

"""Validation script for sidecar container job workload in XMC.

Asserts that the Sidecar Container Job experiment and its associated WorkUnit
were correctly created, configured with sidecar containers, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.sidecar_verify --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

EXPECTED_TITLE_PATTERN: str = "Sidecar"
DEFAULT_QUEUE: str = "cpu-queue"
DEFAULT_TIMEOUT_SEC: int = 600
EXPECTED_JOB_NAME: str = "sidecar-worker"
EXPECTED_SIDECAR: str = "sidecar-service"
EXPECTED_PRIMARY_SPEC: str = "primary-worker"


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
    print(f"\n[2/3] Asserting WorkUnits targeted at '{expected_queue}'...")
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=1,
        expected_queue=expected_queue,
        expected_worker_names=[EXPECTED_JOB_NAME],
        timeout_sec=timeout_sec,
    )
    for wu in wu_results:
        print(
            f"  [✓] WorkUnit #{wu['id']}: Worker='{wu['worker_name']}', Queue='{wu['queue']}', State='{wu['state']}'"
        )
    return wu_results


def _assert_jobset_topology(stub: Any, xid: str) -> None:
    """Validates Sidecar JobSet structure."""
    print("\n[3/3] Validating Sidecar JobSet structure...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    for raw_wu in raw_work_units:
        xmc_entities_validator.validate_jobsets(
            work_unit=raw_wu,
            expected_jobs=[
                {
                    "name": EXPECTED_JOB_NAME,
                    "replicas": 1,
                    "sidecars": [EXPECTED_SIDECAR],
                    "primary_container": EXPECTED_PRIMARY_SPEC,
                }
            ],
        )
        print(f"  [✓] Verified sidecar container '{EXPECTED_SIDECAR}' on WorkUnit: {raw_wu.name}")


def _print_banner(xid: str, expected_queue: str, timeout_sec: int) -> None:
    """Prints verification banner."""
    common.print_banner(
        "XMC Sidecar Container Job Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected Target Queue", expected_queue),
            ("Expected Job Name", EXPECTED_JOB_NAME),
            ("Expected Sidecar", EXPECTED_SIDECAR),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_summary(experiment: Any, expected_queue: str, ui_base_url: str, xid: str) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL SIDECAR JOB ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
            ("Sidecar Container", EXPECTED_SIDECAR),
            ("Primary Container", EXPECTED_PRIMARY_SPEC),
            ("Target Queue", expected_queue),
            ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for sidecar workload."""
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
        _assert_work_units(stub, xid, expected_queue, timeout_sec)
        _assert_jobset_topology(stub, xid)
        _print_summary(experiment, expected_queue, ui_base_url, xid)


if __name__ == "__main__":
    common.run_main(main)
