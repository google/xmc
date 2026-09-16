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

"""Validation script for nightly demonstration workload in XMC.

Concrete test case asserting that the demo workload experiment and its associated
WorkUnits were correctly created, scheduled to the expected queue, and executed
to completion.

Usage:
    python3 -m tools.nightly_tests.demo_workload_verify --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

common.flags.mark_flag_as_required("xid")

# Domain assertion constants for the nightly demo workload test case
EXPECTED_WORK_UNITS: int = 3
EXPECTED_QUEUE: str = "cpu-queue"
EXPECTED_TITLE_PATTERN: str = "Demo Workload"
EXPECTED_WORKER_NAMES: tuple[str, ...] = (
    "demo-worker-1",
    "demo-worker-2",
    "demo-worker-3",
)
TIMEOUT_SEC: int = 600


def main(argv: Sequence[str]) -> None:
    """Main verification routine."""
    common.validate_argv(argv)

    xid = common.parse_xid(common.get_flag(FLAGS, "xid"))
    ui_base_url = common.resolve_ui_base_url()

    common.print_banner(
        "XMC Nightly Workload Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", EXPECTED_QUEUE),
            ("Expected Worker Names", list(EXPECTED_WORKER_NAMES)),
            ("Timeout", f"{TIMEOUT_SEC}s"),
        ],
    )

    with (
        common.handle_verification_errors(),
        common.ess_channel_context(common.get_flag(FLAGS, "endpoint")) as (stub, _),
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
        print(f"  [✓] Experiment Creator: {getattr(experiment, 'creator', 'N/A')}")

        # 2. Assert WorkUnits & States
        print(
            f"\n[2/3] Asserting {EXPECTED_WORK_UNITS} WorkUnits targeted at '{EXPECTED_QUEUE}'..."
        )
        wu_results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid=xid,
            expected_count=EXPECTED_WORK_UNITS,
            expected_queue=EXPECTED_QUEUE,
            expected_worker_names=EXPECTED_WORKER_NAMES,
            timeout_sec=TIMEOUT_SEC,
            wait_for_completion=common.get_flag(FLAGS, "wait_for_completion", True),
        )

        for wu_info in wu_results:
            print(
                f"  [✓] WorkUnit #{wu_info['id']}: Worker='{wu_info['worker_name']}', "
                f"Queue='{wu_info['queue']}', State='{wu_info['state']}'"
            )

        # 3. Print Final Report
        print("\n[3/3] Validation Summary:")
        common.print_success_summary(
            "ALL NIGHTLY WORKLOAD ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
                ("Total Verified WorkUnits", f"{len(wu_results)} / {EXPECTED_WORK_UNITS}"),
                ("Target Queue", EXPECTED_QUEUE),
                ("Expected Worker Names", ", ".join(EXPECTED_WORKER_NAMES)),
                ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
