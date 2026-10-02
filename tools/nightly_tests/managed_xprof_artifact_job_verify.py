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
    python3 -m tools.nightly_tests.managed_xprof_artifact_job_verify --xid=25
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common

EXPECTED_TITLE_PATTERN: str = "ManagedXprof WorkUnit Artifact Example"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("programmatic-xprof-worker",)
EXPECTED_ARTIFACT_TYPES: tuple[str, ...] = ("managed_xprof",)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "programmatic-xprof-worker",
        "replicas": 1,
        "spec": "programmatic-xprof-worker",
    }
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for managed_xprof_artifact_job workload."""
    common.verify_standard_workload(
        argv,
        banner_title="XMC ManagedXprof Artifact Job Verification & Assertions",
        summary_title="ALL MANAGED XPROF ARTIFACT ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        expected_artifact_types=EXPECTED_ARTIFACT_TYPES,
    )


if __name__ == "__main__":
    common.run_main(main)
