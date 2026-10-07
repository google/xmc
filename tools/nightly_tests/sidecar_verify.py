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

"""Validation script for sidecar container job workload in XMC.

Asserts that the Sidecar Container Job experiment and its associated WorkUnit
were correctly created, configured with sidecar containers, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.sidecar_verify --xid=16
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Sidecar"
EXPECTED_JOB_NAME: str = "sidecar-worker"
EXPECTED_INIT_CONTAINER: str = "init-setup"
EXPECTED_SIDECAR: str = "sidecar-service"
EXPECTED_PRIMARY_SPEC: str = "primary-worker"
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": EXPECTED_JOB_NAME,
        "replicas": 1,
        "init_containers": [EXPECTED_INIT_CONTAINER],
        "sidecars": [EXPECTED_SIDECAR],
        "primary_container": EXPECTED_PRIMARY_SPEC,
    }
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for sidecar workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Sidecar Container Job Verification & Assertions",
        summary_title="ALL SIDECAR JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
