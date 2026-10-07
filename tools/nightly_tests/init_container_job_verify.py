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

"""Validation script for init container job workload in XMC.

Asserts that the Init Container Job experiment and its associated WorkUnit
were correctly created, configured with init containers, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.init_container_job_verify --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Init Container Validation Experiment"
EXPECTED_JOB_NAME: str = "init-worker"
EXPECTED_INIT_CONTAINER: str = "init-setup"
EXPECTED_PRIMARY_SPEC: str = "primary-worker"
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": EXPECTED_JOB_NAME,
        "replicas": 1,
        "init_containers": [EXPECTED_INIT_CONTAINER],
        "primary_container": EXPECTED_PRIMARY_SPEC,
    }
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for init_container_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Init Container Job Verification & Assertions",
        summary_title="ALL INIT CONTAINER JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
