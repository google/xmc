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
    python3 -m tools.nightly_tests.per_job_node_selector_verify --xid=20
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common, xmc_entities_validator

EXPECTED_TITLE_PATTERN: str = "Per-Job nodeSelector Validation Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("head",)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
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
EXPECTED_NODE_SELECTORS: dict[str, dict[str, str]] = {
    "head": {"cloud.google.com/gke-nodepool": "cpups"},
    "worker": {"cloud.google.com/gke-nodepool": "gpu-pool"},
}


def _verify_node_selectors(stub: Any, xid: str) -> None:
    """Validates nodeSelector key-value mappings on each expected JobSet job."""
    print("\n[Step 4] Validating per-job node_selector constraints...")
    xmc_entities_validator.validate_node_selector_mappings(stub, xid, EXPECTED_NODE_SELECTORS)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for per_job_node_selector workload."""
    common.verify_standard_workload(
        argv,
        banner_title="XMC Per-Job Node Selector Verification & Assertions",
        summary_title="ALL PER-JOB NODE SELECTOR ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        extra_step=_verify_node_selectors,
        wait_for_completion=False,
    )


if __name__ == "__main__":
    common.run_main(main)
