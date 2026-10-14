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

"""Validation script for metrics dashboard workload in XMC.

Asserts that the Metrics Dashboard End-to-End validation experiment and its WorkUnit
were correctly created, scheduled to the expected queue, configured with metrics worker
JobSet specifications, executed to completion, and optionally verifies the created Dashboard.

Usage:
    python3 -m tools.nightly_tests.examples_verification.metrics_dashboard_job_verify --xid=23
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.examples_verification import metrics_dashboard_ui_verify
from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "Metrics Dashboard End-to-End Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("metrics-worker",)
EXPECTED_METRICS: tuple[str, ...] = (
    "train/loss",
    "train/accuracy",
    "eval/loss",
    "eval/accuracy",
    "perf/step_time_ms",
)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {"name": "metrics-worker", "replicas": 1, "spec": "metrics-worker"}
]


def _verify_metrics_and_dashboard(_stub: Any, xid: str) -> None:
    """Verifies ClickHouse metric ingestion, Dashboard REST hydration, and Playwright UI CUJs."""
    workload_verification.assert_clickhouse_metrics(
        xid,
        expected_metric_names=EXPECTED_METRICS,
        expected_min_rows=50,
        timeout_sec=verifier_cli.get_flag(verifier_cli.FLAGS, "timeout_sec", 600),
    )
    workload_verification.assert_dashboard_chart_data(
        dashboard_id=f"exp_{xid}_overview",
        min_hydrated_points=1,
    )
    if ui_url := verifier_cli.get_flag(verifier_cli.FLAGS, "ui_base_url"):
        with metrics_dashboard_ui_verify.Session(
            ui_base_url=ui_url,
            auth_token=verifier_cli.get_flag(verifier_cli.FLAGS, "auth_token"),
            headless=verifier_cli.get_flag(verifier_cli.FLAGS, "headless", True),
            screenshots_dir=verifier_cli.get_flag(verifier_cli.FLAGS, "screenshots_dir"),
        ) as session:
            metrics_dashboard_ui_verify.verify_metrics_dashboard_ui(session, xid)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for metrics_dashboard_job workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Metrics Dashboard Job Verification & Assertions",
        summary_title="ALL METRICS DASHBOARD JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        extra_step=_verify_metrics_and_dashboard,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
