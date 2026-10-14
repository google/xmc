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

"""Validation script for Pathways multi-component JobSet workload in XMC.

Asserts that the Pathways validation experiment and its WorkUnit were correctly
created, scheduled to the expected queue (e.g. tpu-queue or cpu-queue), configured
with pathways-head (init containers with container port mappings) and worker daemon roles,
and executed to completion.

Usage:
    python3 -m tools.nightly_tests.examples_verification.pathways_job_verify --xid=26 --expected_queue=tpu-queue
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Pathways Multi-Component JobSet Validation"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("pathways-head",)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "pathways-head",
        "replicas": 1,
        "init_containers": ["pathways-rm", "pathways-proxy"],
        "primary_container": "jax-tpu",
    },
    {
        "name": "worker",
        "replicas": 2,
        "primary_container": "pathways-worker",
    },
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for pathways_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Pathways Multi-Component Job Verification",
        summary_title="ALL PATHWAYS JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        wait_for_completion=False,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
