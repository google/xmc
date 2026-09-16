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
to successful completion.

Usage:
    python3 -m tools.nightly_tests.demo_workload_verify --xid=16
"""

from __future__ import annotations

import time
from collections.abc import Sequence

from absl import flags

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

flags.DEFINE_integer(
    "timeout",
    600,
    "Maximum timeout in seconds to wait for demo workload completion (default: 600s / 10m)",
)
flags.DEFINE_integer(
    "expected_work_units",
    3,
    "Expected number of WorkUnits for the demo workload (default: 3)",
)
flags.DEFINE_string(
    "expected_queue",
    "cpu-queue",
    "Expected target queue name for the demo workload (default: cpu-queue)",
)
flags.DEFINE_string(
    "expected_title_pattern",
    "Demo Workload",
    "Expected title substring pattern for the experiment (default: 'Demo Workload')",
)
flags.DEFINE_integer(
    "poll_interval",
    5,
    "Polling interval in seconds when checking WorkUnit states (default: 5s)",
)

common.flags.mark_flag_as_required("xid")

# Domain assertion constants for the nightly demo workload test case
DEFAULT_EXPECTED_WORK_UNITS: int = 3
DEFAULT_EXPECTED_QUEUE: str = "cpu-queue"
DEFAULT_EXPECTED_TITLE_PATTERN: str = "Demo Workload"
DEFAULT_TIMEOUT_SEC: int = 600
DEFAULT_POLL_INTERVAL_SEC: int = 5


def generate_expected_worker_names(num_work_units: int) -> tuple[str, ...]:
    """Generates expected worker names based on work unit count."""
    if num_work_units <= 1:
        return ("demo-worker",)
    return tuple(f"demo-worker-{i}" for i in range(1, num_work_units + 1))


def main(argv: Sequence[str]) -> None:
    """Main verification routine."""
    common.validate_argv(argv)

    xid = common.parse_xid(common.get_flag(FLAGS, "xid"))
    timeout_sec = int(common.get_flag(FLAGS, "timeout", DEFAULT_TIMEOUT_SEC))
    poll_interval_sec = int(common.get_flag(FLAGS, "poll_interval", DEFAULT_POLL_INTERVAL_SEC))
    expected_work_units = int(
        common.get_flag(FLAGS, "expected_work_units", DEFAULT_EXPECTED_WORK_UNITS)
    )
    expected_queue = str(common.get_flag(FLAGS, "expected_queue", DEFAULT_EXPECTED_QUEUE))
    expected_title_pattern = str(
        common.get_flag(FLAGS, "expected_title_pattern", DEFAULT_EXPECTED_TITLE_PATTERN)
    )
    expected_worker_names = generate_expected_worker_names(expected_work_units)
    ui_base_url = common.resolve_ui_base_url()

    common.print_banner(
        "XMC Nightly Workload Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{expected_title_pattern}'"),
            ("Expected WorkUnits Count", expected_work_units),
            ("Expected Target Queue", expected_queue),
            ("Expected Worker Names", list(expected_worker_names)),
            ("Polling Interval", f"{poll_interval_sec}s"),
            ("Timeout", f"{timeout_sec}s"),
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
            expected_title_pattern=expected_title_pattern,
        )
        print(f"  [✓] Experiment Name   : {experiment.name}")
        print(f"  [✓] Experiment Title  : {experiment.title}")
        print(f"  [✓] Experiment Creator: {getattr(experiment, 'creator', 'N/A')}")

        # 2. Assert WorkUnits & Completion States
        print(
            f"\n[2/3] Asserting {expected_work_units} WorkUnits targeted at '{expected_queue}' "
            f"and waiting for successful completion (timeout: {timeout_sec}s)..."
        )
        start_time = time.monotonic()
        wu_results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid=xid,
            expected_count=expected_work_units,
            expected_queue=expected_queue,
            expected_worker_names=expected_worker_names,
            timeout_sec=timeout_sec,
            poll_interval_sec=poll_interval_sec,
            wait_for_completion=True,
        )
        elapsed_sec = int(time.monotonic() - start_time)

        # Assert all work units reached COMPLETED state
        for wu_info in wu_results:
            wu_state = wu_info.get("state", "UNKNOWN")
            if wu_state != "COMPLETED":
                raise AssertionError(
                    f"WorkUnit #{wu_info['id']} ({wu_info['worker_name']}) ended in unexpected "
                    f"state '{wu_state}' (expected 'COMPLETED')."
                )
            print(
                f"  [✓] WorkUnit #{wu_info['id']}: Worker='{wu_info['worker_name']}', "
                f"Queue='{wu_info['queue']}', State='{wu_state}'"
            )

        print(f"\n  [✓] All {len(wu_results)} WorkUnits completed successfully in ~{elapsed_sec}s!")

        # Re-fetch experiment to verify post-completion state
        experiment_final = xmc_entities_validator.validate_experiment(
            stub=stub,
            xid=xid,
            expected_title_pattern=expected_title_pattern,
        )

        # 3. Print Final Report
        print("\n[3/3] Validation Summary:")
        common.print_success_summary(
            "ALL NIGHTLY WORKLOAD ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Experiment", f"{experiment_final.name} (Title: '{experiment_final.title}')"),
                (
                    "Total Verified WorkUnits",
                    f"{len(wu_results)} / {expected_work_units} (All COMPLETED)",
                ),
                ("Target Queue", expected_queue),
                ("Expected Worker Names", ", ".join(expected_worker_names)),
                ("Execution Time", f"~{elapsed_sec}s (Timeout: {timeout_sec}s)"),
                ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
