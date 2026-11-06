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

"""Validation script for multi-pod and multi-container workload in XMC.

Asserts that the Multi-Pod & Sidecar validation experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with multiple
distinct pod roles inside the JobSet, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.examples_verification.multipod_job_test --xid=19
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Multi-Pod & Sidecar Validation Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("counter-workers",)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "counter-workers",
        "replicas": 2,
        "sidecars": ["sidecar-counter"],
        "primary_container": "primary-counter",
    },
    {
        "name": "secondary-counter",
        "replicas": 1,
        "primary_container": "secondary-counter",
    },
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for multipod_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Multi-Pod Job Verification & Assertions",
        summary_title="ALL MULTI-POD JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
