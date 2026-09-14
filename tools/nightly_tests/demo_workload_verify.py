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
    python3 tools/nightly_tests/demo_workload_verify.py --xid=16
"""

from __future__ import annotations

import os
import sys
from collections.abc import Sequence

# Domain assertion constants for the nightly demo workload test case
EXPECTED_WORK_UNITS: int = 3
EXPECTED_QUEUE: str = "cpu-queue"
EXPECTED_TITLE_PATTERN: str = "Demo Workload"
EXPECTED_WORKER_NAMES: tuple[str, ...] = (
    "demo-worker-1",
    "demo-worker-2",
    "demo-worker-3",
)
TIMEOUT_SEC: int = 300

try:
    from absl import app, flags

    FLAGS = flags.FLAGS
    flags.DEFINE_string("xid", None, "Experiment XID to validate (required)")
    flags.mark_flag_as_required("xid")
except ImportError:
    app = None
    flags = None
    FLAGS = None

# Support multiple execution contexts: Bazel sandboxes (tools.nightly_tests),
# package imports (.xmc_entities_validator), and direct standalone script execution.
try:
    from tools.nightly_tests import xmc_entities_validator
except ImportError:
    try:
        from . import xmc_entities_validator  # type: ignore
    except ImportError:
        import xmc_entities_validator  # type: ignore


def main(argv: Sequence[str]) -> None:
    """Main verification routine."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    if FLAGS is None:
        raise RuntimeError("absl is required to parse command-line flags.")

    xid = FLAGS.xid.strip()
    ui_base_url = xmc_entities_validator.resolve_ui_base_url()

    print("=================================================================")
    print("       XMC Nightly Workload Verification & Assertions            ")
    print("=================================================================")
    print(f"Experiment XID          : {xid}")
    print(f"Expected Title Pattern  : '{EXPECTED_TITLE_PATTERN}'")
    print(f"Expected WorkUnits Count: {EXPECTED_WORK_UNITS}")
    print(f"Expected Target Queue   : {EXPECTED_QUEUE}")
    print(f"Expected Worker Names   : {list(EXPECTED_WORKER_NAMES)}")
    print(f"Timeout                 : {TIMEOUT_SEC}s")
    print("-----------------------------------------------------------------")

    endpoint = os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")
    print(f"Connecting to Experiment State Server at: {endpoint} ...")
    channel = None

    try:
        stub, channel = xmc_entities_validator.create_authenticated_stub(endpoint)

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
        )

        for wu_info in wu_results:
            print(
                f"  [✓] WorkUnit #{wu_info['id']}: Worker='{wu_info['worker_name']}', "
                f"Queue='{wu_info['queue']}', State='{wu_info['state']}'"
            )

        # 3. Print Final Report
        print("\n[3/3] Validation Summary:")
        print("=================================================================")
        print("🎉 ALL NIGHTLY WORKLOAD ASSERTIONS PASSED SUCCESSFULLY!")
        print("=================================================================")
        print(f"  • Experiment: {experiment.name} (Title: '{experiment.title}')")
        print(f"  • Total Verified WorkUnits: {len(wu_results)} / {EXPECTED_WORK_UNITS}")
        print(f"  • Target Queue            : {EXPECTED_QUEUE}")
        print(f"  • Expected Worker Names   : {', '.join(EXPECTED_WORKER_NAMES)}")
        print(f"  • Web UI Link             : {ui_base_url}/experiments/{xid}")
        print("=================================================================\n")

    except (AssertionError, TimeoutError) as e:
        print(f"\n❌ ASSERTION FAILED: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"\n💥 UNEXPECTED ERROR during verification: {e}", file=sys.stderr)
        sys.exit(2)
    finally:
        if channel is not None and hasattr(channel, "close"):
            channel.close()


if __name__ == "__main__":
    if app is not None:
        app.run(main)
    else:
        print("absl is required to run as standalone CLI script.", file=sys.stderr)
        sys.exit(1)
