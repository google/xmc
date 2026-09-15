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
    python3 tools/nightly_tests/simple_job_verify.py --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence

# Support multiple execution contexts: Bazel sandbox, relative, and standalone
try:
    from tools.nightly_tests import common, xmc_entities_validator
except ImportError:
    try:
        from . import common, xmc_entities_validator  # type: ignore
    except ImportError:
        import common
        import xmc_entities_validator  # type: ignore

FLAGS = common.FLAGS

# Domain assertion constants for simple_job workload
EXPECTED_TITLE_PATTERN: str = "Demo Workload"
DEFAULT_WORK_UNITS: int = 3
DEFAULT_QUEUE: str = "cpu-queue"
DEFAULT_TIMEOUT_SEC: int = 600


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

    with (
        common.handle_verification_errors(),
        common.ess_channel_context(getattr(FLAGS, "endpoint", None)) as (stub, _),
    ):
        # 1. Assert Experiment Existence & Title
        print(f"\n[1/3] Asserting Experiment metadata for XID {xid}...")
        experiment = xmc_entities_validator.validate_experiment(
            stub=stub,
            xid=xid,
            expected_title_pattern=EXPECTED_TITLE_PATTERN,
        )
        print(f"  [✓] Experiment Name   : {experiment.name}")
        print(f"  [✓] Experiment Title  : {experiment.title}")

        # 2. Assert WorkUnits & States
        print(
            f"\n[2/3] Asserting {expected_work_units} WorkUnits targeted at '{expected_queue}'..."
        )
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

        # 3. Validate JobSet Specifications for each WorkUnit
        print("\n[3/3] Validating JobSet executable specifications...")
        req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
        raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
        for raw_wu in raw_work_units:
            worker_name = xmc_entities_validator.extract_worker_name(raw_wu)
            xmc_entities_validator.validate_jobsets(
                work_unit=raw_wu,
                expected_jobs=[
                    {
                        "name": worker_name,
                        "replicas": 1,
                    }
                ],
            )
            print(f"  [✓] Verified JobSet structure for WorkUnit: {raw_wu.name}")

        # Final Report
        common.print_success_summary(
            "ALL SIMPLE JOB ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
                ("Verified WorkUnits", f"{len(wu_results)} / {expected_work_units}"),
                ("Target Queue", expected_queue),
                ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
