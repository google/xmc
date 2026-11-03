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

"""Validation script for ClickHouse metrics ingestion workload in XMC.

Asserts that the ClickHouse Metrics Ingestion validation experiment and its WorkUnit
were correctly created, scheduled to the expected queue, configured with metric writer
JobSet specifications, executed to completion, verified directly in ClickHouse via SQL queries,
and optionally verifies the created Dashboard.

Note:
    This script serves as the lean baseline verifier for the workload's Kubernetes lifecycle
    and JobSet specification. Comprehensive analytical ClickHouse SQL row-count assertions
    and Dashboard Service hydration validations are implemented in stacked PR #1029
    (tools/nightly_tests/clickhouse_metrics_job_verify.py).

Usage:
    python3 -m tools.nightly_tests.examples_verification.clickhouse_metrics_job_test --xid=22
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "ClickHouse Metrics Ingestion Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("clickhouse-metric-writer",)
DEFAULT_TIMEOUT_SEC: int = 600
EXPECTED_METRIC_SUBSET: set[str] = {"train/loss", "train/accuracy"}
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "clickhouse-metric-writer",
        "replicas": 1,
        "spec": "clickhouse-metric-writer",
    }
]


def _verify_clickhouse_and_dashboard(_stub: Any, xid: str) -> None:
    """Runs ClickHouse SQL metric assertions and Dashboard hydration checks."""
    timeout_sec = verifier_cli.get_flag(verifier_cli.FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    workload_verification.assert_clickhouse_metrics(
        xid,
        expected_metric_names=EXPECTED_METRIC_SUBSET,
        expected_min_rows=1,
        timeout_sec=timeout_sec,
    )
    workload_verification.assert_dashboard_chart_data(dashboard_id=f"exp_{xid}_training_overview")


def main(argv: Sequence[str]) -> None:
    """Main verification routine for clickhouse_metrics_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC ClickHouse Metrics Job Verification & Assertions",
        summary_title="ALL CLICKHOUSE METRICS JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        extra_step=_verify_clickhouse_and_dashboard,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
