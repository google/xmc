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

"""Workload and dashboard verification step runners and orchestrators for XMC nightly tests."""

from __future__ import annotations

import os
import shutil
import subprocess
from collections.abc import Callable, Sequence
from typing import Any

from tools.nightly_tests.verification import (
    endpoint_resolution,
    ess_session,
    verification_report,
    verifier_cli,
    xmc_entities_validator,
)


def assert_experiment(
    stub: Any,
    xid: str,
    expected_title_pattern: str,
    step_label: str = "Step 1",
) -> Any:
    """Asserts experiment existence and title pattern."""
    print(f"\n[{step_label}] Asserting Experiment metadata for XID {xid}...")
    experiment = xmc_entities_validator.validate_experiment(
        stub=stub,
        xid=xid,
        expected_title_pattern=expected_title_pattern,
    )
    print(f"  [✓] Experiment Name   : {experiment.name}")
    print(f"  [✓] Experiment Title  : {experiment.title}")
    return experiment


def assert_work_units(
    stub: Any,
    xid: str,
    expected_count: int,
    expected_queue: str,
    expected_worker_names: Sequence[str],
    timeout_sec: int,
    step_label: str = "Step 2",
    wait_for_completion: bool = True,
) -> list[dict[str, Any]]:
    """Asserts work unit execution state, count, worker names, and queue mapping."""
    print(
        f"\n[{step_label}] Asserting {expected_count} WorkUnit(s) targeted at '{expected_queue}'..."
    )
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=expected_count,
        expected_queue=expected_queue,
        expected_worker_names=expected_worker_names,
        timeout_sec=timeout_sec,
        wait_for_completion=wait_for_completion,
    )
    for wu in wu_results:
        print(
            f"  [✓] WorkUnit #{wu['id']}: Worker='{wu['worker_name']}', "
            f"Queue='{wu['queue']}', State='{wu['state']}'"
        )
    return wu_results


def assert_work_unit_jobsets(
    stub: Any,
    xid: str,
    expected_jobs: Sequence[dict[str, Any]] | None,
    step_label: str = "Step 3",
) -> list[dict[str, Any]]:
    """Validates the first WorkUnit's JobSet topology (homogeneous across standard WorkUnits)."""
    if not expected_jobs:
        return []
    print(f"\n[{step_label}] Validating JobSet specifications...")
    job_results = xmc_entities_validator.validate_work_unit_jobsets(
        stub=stub,
        xid=xid,
        expected_jobs=expected_jobs,
    )
    for job in job_results:
        print(
            f"  [✓] Job '{job['name']}': Replicas={job['replicas']}, "
            f"PrimaryContainer='{job['primary_container']}'"
        )
    return job_results


def cluster_has_managed_xprof_bucket(
    namespace: str | None = None,
    timeout_sec: int = 10,
) -> bool:
    """Returns True when the target cluster renders ConfigMap managed-xprof-gcs-config."""
    if not shutil.which("kubectl"):
        return False
    target_ns = namespace or os.environ.get("K8S_NAMESPACE", "xmc")
    try:
        res = subprocess.run(
            [
                "kubectl",
                "get",
                "configmap",
                "managed-xprof-gcs-config",
                "-n",
                target_ns,
                "-o",
                "jsonpath={.data.gcs_path}",
            ],
            capture_output=True,
            text=True,
            check=False,
            timeout=timeout_sec,
        )
    except (OSError, subprocess.SubprocessError):
        return False
    return res.returncode == 0 and res.stdout.strip().startswith("gs://")


def resolve_require_managed_xprof_url(expected_artifact_types: Sequence[str]) -> bool:
    """Resolves whether ManagedXprof artifacts must expose a non-empty xprof_url."""
    normalized = {
        xmc_entities_validator.normalize_artifact_type(t) for t in expected_artifact_types
    }
    if "managed_xprof" not in normalized:
        return False
    mode = str(
        verifier_cli.get_flag(verifier_cli.FLAGS, "require_managed_xprof_url", "auto") or "auto"
    ).lower()
    if mode == "true":
        return True
    if mode == "false":
        return False
    return cluster_has_managed_xprof_bucket()


def assert_work_unit_artifacts(
    stub: Any,
    xid: str,
    wid: str,
    expected_artifact_types: Sequence[str],
    expected_metric_names: Sequence[str] | None = None,
    timeout_sec: int = 60,
    step_label: str = "Step 4",
) -> list[dict[str, Any]]:
    """Asserts WorkUnit artifact payloads and metric names."""
    print(f"\n[{step_label}] Validating WorkUnit Artifacts (WID: {wid})...")
    require_managed_xprof_url = resolve_require_managed_xprof_url(expected_artifact_types)
    artifact_results = xmc_entities_validator.validate_artifacts(
        stub=stub,
        xid=xid,
        wid=wid,
        expected_artifact_types=expected_artifact_types,
        expected_metric_names=expected_metric_names,
        timeout_sec=min(60, timeout_sec),
        require_managed_xprof_url=require_managed_xprof_url,
    )
    for art in artifact_results:
        print(
            f"  [✓] Artifact: Name='{art['name']}', Type='{art['type']}', "
            f"Title='{art['title']}', Metrics={art['metric_names']}, URL='{art['url']}'"
        )
    return artifact_results


def assert_clickhouse_metrics(
    xid: str,
    expected_metric_names: Sequence[str] | set[str],
    expected_min_rows: int = 1,
    timeout_sec: int = 60,
    step_label: str = "Step 4",
) -> dict[str, Any]:
    """Polls and asserts metric row arrival and value validity directly via ClickHouse SQL."""
    endpoint = endpoint_resolution.resolve_clickhouse_endpoint(
        verifier_cli.get_flag(verifier_cli.FLAGS, "clickhouse_endpoint")
    )
    strict_required = bool(
        verifier_cli.get_flag(verifier_cli.FLAGS, "require_clickhouse_metrics", False)
    )
    print(f"\n[{step_label}] Validating ClickHouse SQL metric rows for XID {xid} via {endpoint}...")
    return xmc_entities_validator.validate_clickhouse_metrics(
        endpoint=endpoint,
        xid=xid,
        expected_metric_names=expected_metric_names,
        expected_min_rows=expected_min_rows,
        timeout_sec=timeout_sec,
        strict_required=strict_required,
    )


def assert_dashboard_chart_data(
    dashboard_id: str,
    min_hydrated_points: int = 1,
    timeout_sec: int = 20,
    step_label: str = "Step 5",
    expected_chart_id: str | None = None,
) -> dict[str, Any]:
    """Verifies Dashboard metadata and GetChartData hydration via Dashboard Service."""
    raw = (
        verifier_cli.get_flag(verifier_cli.FLAGS, "dashboard_endpoint")
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dash_ep = endpoint_resolution.resolve_dashboard_endpoint(raw)
    strict_required = bool(
        verifier_cli.get_flag(verifier_cli.FLAGS, "require_clickhouse_metrics", False) or raw
    )
    print(f"\n[{step_label}] Verifying Dashboard '{dashboard_id}' & GetChartData via {dash_ep}...")
    return xmc_entities_validator.validate_dashboard_chart_data(
        base_endpoint=dash_ep,
        dashboard_id=dashboard_id,
        expected_chart_id=expected_chart_id,
        min_hydrated_points=min_hydrated_points,
        timeout_sec=timeout_sec,
        strict_required=strict_required,
    )


def _print_workload_banner(
    banner_title: str,
    xid: str,
    expected_title: str,
    queue: str,
    workers: Sequence[str],
    timeout: int,
) -> None:
    """Prints the standard workload verification header banner."""
    verification_report.print_banner(
        banner_title,
        {
            "XID": xid,
            "Title": expected_title,
            "Queue": queue,
            "WorkUnits": len(workers),
            "Workers": workers,
            "Timeout": f"{timeout}s",
        },
    )


def _print_workload_summary(summary_title: str, exp: Any, xid: str, queue: str) -> None:
    """Prints the standard workload verification success summary."""
    ui_url = f"{endpoint_resolution.resolve_ui_base_url()}/experiments/{xid}"
    details = {"Experiment": exp.name, "Title": exp.title, "Queue": queue, "UI": ui_url}
    verification_report.print_success_summary(summary_title, details)


def verify_standard_workload(
    argv: Sequence[str],
    banner_title: str,
    summary_title: str,
    expected_title_pattern: str,
    expected_jobs: Sequence[dict[str, Any]] | None = None,
    expected_worker_names: Sequence[str] | None = None,
    expected_artifact_types: Sequence[str] | None = None,
    expected_metric_names: Sequence[str] | None = None,
    extra_step: Callable[[Any, str], None] | None = None,
    wait_for_completion: bool = True,
) -> None:
    """Orchestrates standard Experiment, WorkUnit, JobSet, and Artifact verification."""
    verifier_cli.validate_argv(argv)
    xid = verifier_cli.parse_xid(verifier_cli.get_flag(verifier_cli.FLAGS, "xid"))
    queue = verifier_cli.get_flag(verifier_cli.FLAGS, "expected_queue", "cpu-queue")
    title_pattern = (
        verifier_cli.get_flag(verifier_cli.FLAGS, "expected_title_pattern", expected_title_pattern)
        or expected_title_pattern
    )
    workers = expected_worker_names or ([expected_jobs[0]["name"]] if expected_jobs else [])
    timeout = verifier_cli.get_flag(verifier_cli.FLAGS, "timeout_sec", 600)
    _print_workload_banner(banner_title, xid, title_pattern, queue, workers, timeout)
    ep = verifier_cli.get_flag(verifier_cli.FLAGS, "endpoint")
    with (
        ess_session.handle_verification_errors(),
        ess_session.ess_channel_context(ep) as (stub, _),
    ):
        try:
            exp = assert_experiment(stub, xid, title_pattern)
            wus = assert_work_units(
                stub,
                xid,
                len(workers),
                queue,
                workers,
                timeout,
                wait_for_completion=wait_for_completion,
            )
            assert_work_unit_jobsets(stub, xid, expected_jobs)
            if expected_artifact_types:
                assert_work_unit_artifacts(
                    stub, xid, wus[0]["id"], expected_artifact_types, expected_metric_names, timeout
                )
            if extra_step:
                extra_step(stub, xid)
            _print_workload_summary(summary_title, exp, xid, queue)
        finally:
            if not wait_for_completion:
                xmc_entities_validator.stop_work_units(stub, xid)


def assert_dashboard(
    dashboard_endpoint: str,
    dashboard_id: str,
    expected_title_pattern: str,
    min_charts: int = 1,
    timeout_sec: int = 120,
) -> dict[str, Any]:
    """Queries and validates a standalone dashboard and its constituent charts via REST API."""
    print(f"\n[1/2] Querying and validating Dashboard '{dashboard_id}'...")
    spec = {
        "dashboard_id": dashboard_id,
        "expected_title": expected_title_pattern,
        "min_charts": min_charts,
    }
    dash_results = xmc_entities_validator.validate_dashboard(
        endpoint=dashboard_endpoint, expected_dashboards=[spec], timeout_sec=timeout_sec
    )
    if not dash_results:
        raise AssertionError(f"Dashboard '{dashboard_id}' was not returned by endpoint.")
    dash_info = dash_results[0]
    print(f"  [✓] Dashboard ID      : {dash_info.get('id') or dash_info.get('dashboard_id', '')}")
    print(f"  [✓] Dashboard Title   : {dash_info['title']}")
    print(f"  [✓] Referenced Charts : {len(dash_info.get('chart_ids', []))}")
    print("\n[2/2] Validating Constituent Charts...")
    for chart in dash_info.get("charts", []):
        cid = chart.get("name", chart.get("id", "unknown"))
        print(f"  [✓] Chart '{cid}': Title='{chart.get('title', '')}'")
    return dash_info


def _print_dashboard_banner(
    banner_title: str,
    dashboard_id: str,
    expected_title_pattern: str,
    dashboard_endpoint: str,
    timeout_sec: int,
) -> None:
    """Prints the standalone dashboard verification header banner."""
    verification_report.print_banner(
        banner_title,
        [
            ("Dashboard ID", dashboard_id),
            ("Expected Title Pattern", f"'{expected_title_pattern}'"),
            ("Dashboard REST Endpoint", dashboard_endpoint),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_dashboard_summary(
    summary_title: str, dashboard_id: str, dash_info: dict[str, Any]
) -> None:
    """Prints the standalone dashboard verification success summary."""
    verification_report.print_success_summary(
        summary_title,
        [
            ("Dashboard ID", dashboard_id),
            ("Dashboard Title", dash_info["title"]),
            ("Validated Charts", len(dash_info.get("charts", []))),
            (
                "Web UI Link",
                f"{endpoint_resolution.resolve_ui_base_url()}/dashboards/{dashboard_id}",
            ),
        ],
    )


def verify_dashboard_workload(
    argv: Sequence[str],
    banner_title: str,
    summary_title: str,
    default_dashboard_id: str,
    expected_title_pattern: str,
    expected_min_charts: int = 1,
    default_timeout_sec: int = 120,
) -> None:
    """Orchestrates standalone Dashboard and constituent Chart verification via REST API."""
    verifier_cli.validate_argv(argv)
    raw_id = verifier_cli.get_flag(verifier_cli.FLAGS, "dashboard_id", None) or (
        verifier_cli.get_flag(verifier_cli.FLAGS, "xid", None)
    )
    parsed_id = verifier_cli.parse_xid(raw_id, required=False) or default_dashboard_id
    dashboard_id = f"{default_dashboard_id}_{parsed_id}" if parsed_id.isdigit() else parsed_id
    title_pattern = verifier_cli.get_flag(
        verifier_cli.FLAGS, "expected_title_pattern", expected_title_pattern
    )
    timeout_sec = verifier_cli.get_flag(verifier_cli.FLAGS, "timeout_sec", default_timeout_sec)
    raw_ep = (
        verifier_cli.get_flag(verifier_cli.FLAGS, "dashboard_endpoint", None)
        or os.environ.get("XMC_DASHBOARD_REST_ENDPOINT")
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dash_ep = endpoint_resolution.resolve_dashboard_endpoint(raw_ep)
    _print_dashboard_banner(banner_title, dashboard_id, title_pattern, dash_ep, timeout_sec)
    with ess_session.handle_verification_errors():
        dash_info = assert_dashboard(
            dash_ep, dashboard_id, title_pattern, expected_min_charts, timeout_sec
        )
        _print_dashboard_summary(summary_title, dashboard_id, dash_info)
