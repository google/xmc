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
    python3 -m tools.nightly_tests.clickhouse_metrics_job_verify --xid=22
"""

from __future__ import annotations

import os
from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

EXPECTED_TITLE_PATTERN: str = "ClickHouse Metrics Ingestion Experiment"
EXPECTED_WORK_UNITS: int = 1
DEFAULT_QUEUE: str = "cpu-queue"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("clickhouse-metric-writer",)
DEFAULT_TIMEOUT_SEC: int = 600
EXPECTED_METRIC_SUBSET: set[str] = {"train/loss", "train/accuracy"}


def _assert_experiment(stub: Any, xid: str) -> Any:
    """Asserts experiment existence and title pattern."""
    print(f"\n[1/5] Asserting Experiment metadata for XID {xid}...")
    experiment = xmc_entities_validator.validate_experiment(
        stub=stub,
        xid=xid,
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
    )
    print(f"  [✓] Experiment Name   : {experiment.name}")
    print(f"  [✓] Experiment Title  : {experiment.title}")
    return experiment


def _assert_work_units(
    stub: Any, xid: str, expected_queue: str, timeout_sec: int
) -> list[dict[str, Any]]:
    """Asserts work unit execution state and queue mapping."""
    print(f"\n[2/5] Asserting {EXPECTED_WORK_UNITS} WorkUnit targeted at '{expected_queue}'...")
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=EXPECTED_WORK_UNITS,
        expected_queue=expected_queue,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        timeout_sec=timeout_sec,
    )
    for wu in wu_results:
        print(
            f"  [✓] WorkUnit #{wu['id']}: Worker='{wu['worker_name']}', Queue='{wu['queue']}', State='{wu['state']}'"
        )
    return wu_results


def _assert_jobset(stub: Any, xid: str) -> list[dict[str, Any]]:
    """Validates JobSet structure."""
    print("\n[3/5] Validating JobSet structure...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if not raw_work_units:
        raise AssertionError(f"No WorkUnits found for experiment {xid}.")
    return xmc_entities_validator.validate_jobsets(
        work_unit=raw_work_units[0],
        expected_jobs=[
            {
                "name": "clickhouse-metric-writer",
                "replicas": 1,
                "spec": "clickhouse-metric-writer",
            }
        ],
    )


def _assert_clickhouse_sql_metrics(
    xid: str,
    timeout_sec: int = 60,
    poll_interval_sec: int = 3,
) -> dict[str, Any]:
    """Polls and asserts metric row arrival and value validity directly via ClickHouse SQL."""
    raw_endpoint = common.get_flag(FLAGS, "clickhouse_endpoint", None)
    endpoint = common.resolve_clickhouse_endpoint(raw_endpoint)
    strict_required = bool(common.get_flag(FLAGS, "require_clickhouse_metrics", False))
    print(f"\n[4/5] Validating ClickHouse SQL metric rows for XID {xid} via {endpoint}...")

    return xmc_entities_validator.validate_clickhouse_metrics(
        endpoint=endpoint,
        xid=xid,
        expected_metric_names=EXPECTED_METRIC_SUBSET,
        expected_min_rows=1,
        timeout_sec=timeout_sec,
        poll_interval_sec=poll_interval_sec,
        strict_required=strict_required,
    )


def _check_optional_dashboard(
    xid: str, timeout_sec: int = 20, poll_interval_sec: int = 2
) -> dict[str, Any]:
    """Verifies Dashboard metadata and GetChartData hydration via Dashboard Service."""
    raw_endpoint = (
        common.get_flag(FLAGS, "dashboard_endpoint", None)
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    strict_required = bool(common.get_flag(FLAGS, "require_clickhouse_metrics", False)) or bool(
        raw_endpoint
    )
    dash_endpoint = common.resolve_dashboard_endpoint(raw_endpoint)
    dashboard_id = f"exp_{xid}_training_overview"
    print(f"\n[5/5] Verifying Dashboard & GetChartData for XID {xid} via {dash_endpoint}...")

    return xmc_entities_validator.validate_dashboard_chart_data(
        base_endpoint=dash_endpoint,
        dashboard_id=dashboard_id,
        min_hydrated_points=1,
        timeout_sec=timeout_sec,
        poll_interval_sec=poll_interval_sec,
        strict_required=strict_required,
    )


def _print_banner(xid: str, expected_queue: str, timeout_sec: int) -> None:
    """Prints verification header banner."""
    common.print_banner(
        "XMC ClickHouse Metrics Job Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", expected_queue),
            ("Expected Worker Names", list(EXPECTED_WORKER_NAMES)),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_summary(
    experiment: Any, wu_count: int, expected_queue: str, ui_base_url: str, xid: str
) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL CLICKHOUSE METRICS JOB ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
            ("Verified WorkUnits", f"{wu_count} / {EXPECTED_WORK_UNITS}"),
            ("Target Queue", expected_queue),
            ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for clickhouse_metrics_job workload."""
    common.validate_argv(argv)
    xid = common.parse_xid(common.get_flag(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()

    _print_banner(xid, expected_queue, timeout_sec)
    with (
        common.handle_verification_errors(),
        common.ess_channel_context(common.get_flag(FLAGS, "endpoint", None)) as (stub, _),
    ):
        experiment = _assert_experiment(stub, xid)
        wu_results = _assert_work_units(stub, xid, expected_queue, timeout_sec)
        _assert_jobset(stub, xid)
        _assert_clickhouse_sql_metrics(xid, timeout_sec=min(timeout_sec, 30))
        _check_optional_dashboard(xid)
        _print_summary(experiment, len(wu_results), expected_queue, ui_base_url, xid)


if __name__ == "__main__":
    common.run_main(main)
