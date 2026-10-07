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

"""Validation script for metrics autologging workload in XMC.

Asserts that the Metrics System Validation experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with the 4 distinct
metrics logging jobs (Manual SDK, Keras, Keras Callback, Lightning), and executed to completion.

Usage:
    python3 -m tools.nightly_tests.metrics_job_verify --xid=24
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Metrics System Validation Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("metrics-manual",)
EXPECTED_METRICS: tuple[str, ...] = ("manual/loss", "loss", "keras_cb/loss", "pl/loss_epoch")
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {"name": "metrics-manual", "replicas": 1, "spec": "metrics-manual"},
    {"name": "metrics-keras", "replicas": 1, "spec": "metrics-keras"},
    {"name": "metrics-keras-cb", "replicas": 1, "spec": "metrics-keras-cb"},
    {"name": "metrics-lightning", "replicas": 1, "spec": "metrics-lightning"},
]


def _verify_metrics(_stub: Any, xid: str) -> None:
    """Verifies ClickHouse metric ingestion across all 4 autologging strategies."""
    workload_verification.assert_clickhouse_metrics(
        xid,
        expected_metric_names=EXPECTED_METRICS,
        expected_min_rows=25,
        timeout_sec=verifier_cli.get_flag(verifier_cli.FLAGS, "timeout_sec", 600),
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for metrics_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Metrics Autologging Job Verification & Assertions",
        summary_title="ALL METRICS AUTOLOGGING JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        extra_step=_verify_metrics,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
