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

"""Validation script for per-job node selector workload in XMC.

Asserts that the Per-Job nodeSelector validation experiment and its WorkUnit
were correctly created, scheduled to the expected queue, configured with distinct
nodeSelector mappings (head on cpups, workers on gpu-pool), and executed to completion.

Usage:
    python3 tools/nightly_tests/per_job_node_selector_verify.py --xid=20
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

# Domain assertion constants for per_job_node_selector workload
EXPECTED_TITLE_PATTERN: str = "Per-Job nodeSelector Validation Experiment"
EXPECTED_WORK_UNITS: int = 1
DEFAULT_QUEUE: str = "cpu-queue"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("head",)
DEFAULT_TIMEOUT_SEC: int = 600

EXPECTED_JOBSET_SPECS = [
    {
        "name": "head",
        "replicas": 1,
        "primary_container": "coordinator",
    },
    {
        "name": "worker",
        "replicas": 2,
        "primary_container": "worker-processor",
    },
]

EXPECTED_NODE_SELECTORS = {
    "head": {"cloud.google.com/gke-nodepool": "cpups"},
    "worker": {"cloud.google.com/gke-nodepool": "gpu-pool"},
}


def main(argv: Sequence[str]) -> None:
    """Main verification routine for per_job_node_selector workload."""
    common.validate_argv(argv)

    xid = common.parse_xid(getattr(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()

    common.print_banner(
        "XMC Per-Job Node Selector Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", expected_queue),
            ("Expected Primary Worker", list(EXPECTED_WORKER_NAMES)),
            ("Expected Node Selectors", EXPECTED_NODE_SELECTORS),
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
        print(f"\n[2/3] Asserting {EXPECTED_WORK_UNITS} WorkUnit targeted at '{expected_queue}'...")
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

        # 3. Validate JobSet and Node Selector Mappings
        print("\n[3/3] Validating JobSet structure and per-job nodeSelector mappings...")
        req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
        raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
        if not raw_work_units:
            raise AssertionError(f"No WorkUnits found for experiment {xid}.")

        job_results = xmc_entities_validator.validate_jobsets(
            work_unit=raw_work_units[0],
            expected_jobs=EXPECTED_JOBSET_SPECS,
        )

        # Validate node_selector fields on the raw protobuf job objects
        jobset = getattr(raw_work_units[0].executable, "jobset", None)
        actual_jobs = {getattr(j, "name", ""): j for j in getattr(jobset, "jobs", [])}

        for job_name, expected_ns in EXPECTED_NODE_SELECTORS.items():
            actual_job = actual_jobs.get(job_name)
            if actual_job is None:
                raise AssertionError(f"Job '{job_name}' missing from JobSet jobs.")
            actual_ns = dict(getattr(actual_job, "node_selector", {}))
            for k, v in expected_ns.items():
                if actual_ns.get(k) != v:
                    raise AssertionError(
                        f"Job '{job_name}' node_selector['{k}'] mismatch: "
                        f"expected '{v}', found '{actual_ns.get(k)}'."
                    )
            print(f"  [✓] Verified node_selector for '{job_name}': {actual_ns}")

        # Final Report
        common.print_success_summary(
            "ALL PER-JOB NODE SELECTOR ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
                ("Verified WorkUnits", f"{len(wu_results)} / {EXPECTED_WORK_UNITS}"),
                ("Target Queue", expected_queue),
                ("Verified Jobs", [j["name"] for j in job_results]),
                ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
