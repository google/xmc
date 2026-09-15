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

"""Validation script for ManagedXprof artifact workload in XMC.

Asserts that the ManagedXprof WorkUnit Artifact experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with ManagedXprof
artifact payloads attached during execution lifecycle, and executed to completion.

Usage:
    python3 tools/nightly_tests/managed_xprof_artifact_job_verify.py --xid=25
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

# Domain assertion constants for managed_xprof_artifact_job workload
EXPECTED_TITLE_PATTERN: str = "ManagedXprof WorkUnit Artifact Example"
EXPECTED_WORK_UNITS: int = 1
DEFAULT_QUEUE: str = "cpu-queue"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("programmatic-xprof-worker",)
EXPECTED_ARTIFACT_TYPES: tuple[str, ...] = ("managed_xprof",)
DEFAULT_TIMEOUT_SEC: int = 600


def main(argv: Sequence[str]) -> None:
    """Main verification routine for managed_xprof_artifact_job workload."""
    common.validate_argv(argv)

    xid = common.parse_xid(getattr(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()

    common.print_banner(
        "XMC ManagedXprof Artifact Job Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", expected_queue),
            ("Expected Worker Names", list(EXPECTED_WORKER_NAMES)),
            ("Expected Artifact Types", list(EXPECTED_ARTIFACT_TYPES)),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )

    with (
        common.handle_verification_errors(),
        common.ess_channel_context(getattr(FLAGS, "endpoint", None)) as (stub, _),
    ):
        # 1. Assert Experiment Existence & Title
        print(f"\n[1/4] Asserting Experiment metadata for XID {xid}...")
        experiment = xmc_entities_validator.validate_experiment(
            stub=stub,
            xid=xid,
            expected_title_pattern=EXPECTED_TITLE_PATTERN,
        )
        print(f"  [✓] Experiment Name   : {experiment.name}")
        print(f"  [✓] Experiment Title  : {experiment.title}")

        # 2. Assert WorkUnits & States
        print(f"\n[2/4] Asserting {EXPECTED_WORK_UNITS} WorkUnit targeted at '{expected_queue}'...")
        wu_results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid=xid,
            expected_count=EXPECTED_WORK_UNITS,
            expected_queue=expected_queue,
            expected_worker_names=EXPECTED_WORKER_NAMES,
            timeout_sec=timeout_sec,
        )

        for wu_info in wu_results:
            print(
                f"  [✓] WorkUnit #{wu_info['id']}: Worker='{wu_info['worker_name']}', "
                f"Queue='{wu_info['queue']}', State='{wu_info['state']}'"
            )

        # 3. Validate JobSet Specification
        print("\n[3/4] Validating JobSet structure...")
        req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
        raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
        if not raw_work_units:
            raise AssertionError(f"No WorkUnits found for experiment {xid}.")

        job_results = xmc_entities_validator.validate_jobsets(
            work_unit=raw_work_units[0],
            expected_jobs=[
                {
                    "name": "programmatic-xprof-worker",
                    "replicas": 1,
                    "spec": "programmatic-xprof-worker",
                }
            ],
        )
        for job_info in job_results:
            print(
                f"  [✓] Job '{job_info['name']}': Replicas={job_info['replicas']}, "
                f"PrimaryContainer='{job_info['primary_container']}'"
            )

        # 4. Assert Artifacts (ManagedXprofArtifact)
        wid = wu_results[0]["id"]
        print(f"\n[4/4] Validating WorkUnit Artifacts for {raw_work_units[0].name} (WID: {wid})...")
        artifact_results = xmc_entities_validator.validate_artifacts(
            stub=stub,
            xid=xid,
            wid=wid,
            expected_artifact_types=EXPECTED_ARTIFACT_TYPES,
            timeout_sec=min(60, timeout_sec),
        )
        for art in artifact_results:
            print(
                f"  [✓] Artifact: Name='{art['name']}', Type='{art['type']}', "
                f"Title='{art['title']}'"
            )

        # Final Report
        common.print_success_summary(
            "ALL MANAGED XPROF ARTIFACT ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
                ("Verified WorkUnits", f"{len(wu_results)} / {EXPECTED_WORK_UNITS}"),
                ("Target Queue", expected_queue),
                (
                    "Verified Artifacts",
                    f"{len(artifact_results)} ({list(EXPECTED_ARTIFACT_TYPES)})",
                ),
                ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
