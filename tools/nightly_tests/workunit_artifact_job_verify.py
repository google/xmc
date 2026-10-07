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

"""Validation script for WorkUnit artifact lifecycle workload in XMC.

Asserts that the WorkUnit Artifact Lifecycle experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with INPUT
(`code_source`), EXECUTION (`text`), and OUTPUT (`url`) artifact payloads, and
executed to completion.

Usage:
    python3 -m tools.nightly_tests.workunit_artifact_job_verify --xid=26
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "WorkUnit Artifact Lifecycle Example"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("artifact-demo-worker",)
EXPECTED_ARTIFACT_TYPES: tuple[str, ...] = ("code_source", "text", "url")
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "artifact-demo-worker",
        "replicas": 1,
    }
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for workunit_artifact_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC WorkUnit Artifact Lifecycle Job Verification & Assertions",
        summary_title="ALL WORKUNIT ARTIFACT LIFECYCLE ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        expected_artifact_types=EXPECTED_ARTIFACT_TYPES,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
