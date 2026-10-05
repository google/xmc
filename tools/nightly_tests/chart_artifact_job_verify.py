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

"""Validation script for chart artifact workload in XMC.

Asserts that the Chart Artifact validation experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with ChartArtifact
and UrlArtifact payloads (asserting metric names accuracy, learning_rate, loss),
and executed to completion.

Usage:
    python3 -m tools.nightly_tests.chart_artifact_job_verify --xid=21
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common

EXPECTED_TITLE_PATTERN: str = "Chart Artifact Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("chart-worker",)
EXPECTED_ARTIFACT_TYPES: tuple[str, ...] = ("chart", "url")
EXPECTED_METRIC_NAMES: tuple[str, ...] = (
    "chart_demo/accuracy",
    "chart_demo/learning_rate",
    "chart_demo/loss",
)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {"name": "chart-worker", "replicas": 1, "spec": "chart-worker"}
]


def _verify_metrics_and_dashboard(_stub: Any, xid: str) -> None:
    """Verifies ClickHouse metric ingestion and Dashboard chart data hydration."""
    common.assert_clickhouse_metrics(
        xid,
        expected_metric_names=EXPECTED_METRIC_NAMES,
        expected_min_rows=60,
        timeout_sec=common.get_flag(common.FLAGS, "timeout_sec", 600),
        step_label="5/6",
    )
    common.assert_dashboard_chart_data(
        dashboard_id=f"chart_artifact_x{xid}_w1",
        min_hydrated_points=1,
        step_label="6/6",
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for chart_artifact_job workload."""
    common.verify_standard_workload(
        argv,
        banner_title="XMC Chart Artifact Job Verification & Assertions",
        summary_title="ALL CHART ARTIFACT JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        expected_artifact_types=EXPECTED_ARTIFACT_TYPES,
        expected_metric_names=EXPECTED_METRIC_NAMES,
        extra_step=_verify_metrics_and_dashboard,
    )


if __name__ == "__main__":
    common.run_main(main)
