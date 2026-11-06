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
"""Validation script for MultiKueue delegated workload in XMC."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.examples_verification import simple_job_test
from tools.nightly_tests.verification import (
    verifier_cli,
    workload_verification,
    xmc_entities_validator,
)

DEFAULT_QUEUE: str = "multikueue-cpu-queue"
DEFAULT_WORKER_CLUSTER: str = "xmc-nightly-worker-1"
verifier_cli.flags.DEFINE_string(
    "expected_worker_cluster", DEFAULT_WORKER_CLUSTER, "Expected MultiKueue worker cluster."
)


def _assert_multikueue_workload(stub: Any, xid: str) -> None:
    simple_job_test.assert_all_work_unit_specs(stub, xid)
    cluster = verifier_cli.get_flag(
        verifier_cli.FLAGS, "expected_worker_cluster", DEFAULT_WORKER_CLUSTER
    )
    queue = verifier_cli.get_flag(verifier_cli.FLAGS, "expected_queue", DEFAULT_QUEUE)
    xmc_entities_validator.validate_multikueue_execution_metadata(stub, xid, cluster)
    xmc_entities_validator.validate_multikueue_k8s_delegation(xid, queue)


def main(argv: Sequence[str]) -> None:
    cli_flags: Any = verifier_cli.FLAGS
    if "expected_queue" in cli_flags and not cli_flags["expected_queue"].present:
        cli_flags.expected_queue = DEFAULT_QUEUE
    count = verifier_cli.resolve_expected_work_units(simple_job_test.DEFAULT_WORK_UNITS)
    workers = [f"demo-worker-{i}" for i in range(1, count + 1)] if count > 1 else ["demo-worker"]
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC MultiKueue Delegated Job Verification & Assertions",
        summary_title="ALL MULTIKUEUE JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=simple_job_test.EXPECTED_TITLE_PATTERN,
        expected_worker_names=workers,
        extra_step=_assert_multikueue_workload,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
