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

"""Common utilities and verification abstractions for XMC nightly tests.

Provides standard command-line flags, gRPC channel lifecycle managers,
endpoint resolution, argument validation, formatted logging helpers, and
workload verification step runners (`assert_*`, `verify_standard_workload`)
used across XMC verification CLI scripts.

TODO(xmc): Split this module by responsibility (verifier_cli,
endpoint_resolution, ess_session, verification_report, workload_verification)
and delete it. Do not add new helpers here.
"""

from __future__ import annotations

import contextlib
import os
import sys
from collections.abc import Callable, Iterator, Mapping, Sequence
from typing import Any

from absl import app, flags

from tools.nightly_tests import xmc_entities_validator

FLAGS = flags.FLAGS

flags.DEFINE_string(
    "xid",
    None,
    "Experiment XID to validate (required for experiment tests)",
)
flags.DEFINE_string("endpoint", None, "ESS gRPC endpoint (host:port)")
flags.DEFINE_string(
    "expected_queue",
    os.environ.get("QUEUE_NAME", "cpu-queue"),
    "Expected ClusterQueue name",
)
flags.DEFINE_integer("timeout_sec", 600, "Timeout in seconds")
flags.DEFINE_string(
    "dashboard_endpoint",
    None,
    "Dashboard Service endpoint (host:port or URL)",
)
flags.DEFINE_integer(
    "expected_work_units",
    None,
    "Expected number of WorkUnits",
)
flags.DEFINE_string(
    "clickhouse_endpoint",
    None,
    "ClickHouse HTTP endpoint (host:port or URL)",
)
flags.DEFINE_bool(
    "require_clickhouse_metrics",
    False,
    "Whether ClickHouse metric arrival assertion is strictly required",
)
flags.DEFINE_string(
    "dashboard_id",
    None,
    "Resource ID slug for the dashboard",
)
flags.DEFINE_string(
    "expected_title_pattern",
    None,
    "Expected title pattern for the experiment or dashboard",
)


def get_flag(flags_obj: Any, name: str, default: Any = None) -> Any:
    """Safely retrieves a flag value from absl FLAGS or a mock SimpleNamespace."""
    return default if (val := getattr(flags_obj, name, None)) is None else val


def validate_argv(argv: Sequence[str]) -> None:
    """Validates command-line arguments to ensure no extra positional arguments exist."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")


def parse_xid(raw_xid: str | None, required: bool = True) -> str:
    """Normalizes and extracts the numeric or slug XID string."""
    if not raw_xid or not str(raw_xid).strip():
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    xid = str(raw_xid).strip()
    if xid.startswith("/"):
        xid = xid.lstrip("/")
    if xid.startswith("experiments/"):
        xid = xid.split("/")[-1]
    elif xid.startswith("dashboards/"):
        xid = xid.split("dashboards/", 1)[1].strip()
    return xid


def resolve_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ESS gRPC endpoint from parameter, environment, or default."""
    return provided_endpoint or os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")


def resolve_dashboard_endpoint(provided_endpoint: str | None = None) -> str:
    """Derives HTTP REST base endpoint for dashboard validation."""
    if provided_endpoint and str(provided_endpoint).strip():
        ep = str(provided_endpoint).strip()
        if ep.startswith(("http://", "https://")):
            return ep
        return f"http://{ep}" if ep.startswith(("localhost", "127.0.0.1")) else f"https://{ep}"
    return resolve_ui_base_url()


def resolve_clickhouse_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ClickHouse HTTP endpoint from parameter, environment, or default."""
    if provided_endpoint and str(provided_endpoint).strip():
        ep = str(provided_endpoint).strip()
    elif os.environ.get("CLICKHOUSE_ENDPOINT"):
        ep = os.environ["CLICKHOUSE_ENDPOINT"].strip()
    elif os.environ.get("CLICKHOUSE_HOST"):
        host = os.environ["CLICKHOUSE_HOST"].strip()
        port = os.environ.get(
            "CLICKHOUSE_HTTP_PORT", os.environ.get("CLICKHOUSE_PORT", "8123")
        ).strip()
        ep = f"{host}:{port}"
    else:
        ep = "http://127.0.0.1:8123"

    if not ep.startswith(("http://", "https://")):
        ep = f"http://{ep}"
    return ep.rstrip("/")


def resolve_ui_base_url() -> str:
    """Resolves Web UI base URL via xmc_entities_validator."""
    return xmc_entities_validator.resolve_ui_base_url()


def resolve_expected_work_units(default: int = 3) -> int:
    """Resolves expected work units count from flag, NUM_WORK_UNITS env var, or default.

    Args:
        default: Fallback count if neither the flag nor the env var is set.

    Returns:
        The resolved integer number of work units.
    """
    flag_val = get_flag(FLAGS, "expected_work_units", None)
    if flag_val is not None:
        try:
            val = int(flag_val)
            if val > 0:
                return val
        except (ValueError, TypeError):
            pass
    env_val = os.environ.get("NUM_WORK_UNITS")
    if env_val:
        try:
            val = int(env_val)
            if val > 0:
                return val
        except (ValueError, TypeError):
            pass
    return default


@contextlib.contextmanager
def ess_channel_context(endpoint: str | None = None) -> Iterator[tuple[Any, Any]]:
    """Context manager for authenticated ESS gRPC channel and stub lifecycle."""
    resolved_endpoint = resolve_endpoint(endpoint)
    print(f"Connecting to Experiment State Server at: {resolved_endpoint} ...")
    stub, channel = xmc_entities_validator.create_authenticated_stub(resolved_endpoint)
    try:
        yield stub, channel
    finally:
        if channel is not None and hasattr(channel, "close"):
            channel.close()


@contextlib.contextmanager
def handle_verification_errors() -> Iterator[None]:
    """Context manager handling verification assertion errors and unexpected exceptions."""
    try:
        yield
    except (AssertionError, TimeoutError) as e:
        print(f"\n❌ ASSERTION FAILED: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"\n💥 UNEXPECTED ERROR during verification: {e}", file=sys.stderr)
        sys.exit(2)


def print_banner(title: str, details: Mapping[str, Any] | Sequence[tuple[str, Any]]) -> None:
    """Prints a formatted verification header banner."""
    print("=================================================================")
    print(f"       {title.strip()}       ".center(65))
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"{label:<24}: {val_str}")
    print("-----------------------------------------------------------------")


def print_success_summary(
    title: str,
    details: Mapping[str, Any] | Sequence[tuple[str, Any]],
) -> None:
    """Prints a formatted verification success summary."""
    print("\n=================================================================")
    print(f"🎉 {title.strip()}")
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"  • {label:<24}: {val_str}")
    print("=================================================================\n")


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
    artifact_results = xmc_entities_validator.validate_artifacts(
        stub=stub,
        xid=xid,
        wid=wid,
        expected_artifact_types=expected_artifact_types,
        expected_metric_names=expected_metric_names,
        timeout_sec=min(60, timeout_sec),
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
    endpoint = resolve_clickhouse_endpoint(get_flag(FLAGS, "clickhouse_endpoint"))
    strict_required = bool(get_flag(FLAGS, "require_clickhouse_metrics", False))
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
        get_flag(FLAGS, "dashboard_endpoint")
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dash_ep = resolve_dashboard_endpoint(raw)
    strict_required = bool(get_flag(FLAGS, "require_clickhouse_metrics", False) or raw)
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
    print_banner(
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
    ui_url = f"{resolve_ui_base_url()}/experiments/{xid}"
    details = {"Experiment": exp.name, "Title": exp.title, "Queue": queue, "UI": ui_url}
    print_success_summary(summary_title, details)


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
    validate_argv(argv)
    xid = parse_xid(get_flag(FLAGS, "xid"))
    queue = get_flag(FLAGS, "expected_queue", "cpu-queue")
    workers = expected_worker_names or ([expected_jobs[0]["name"]] if expected_jobs else [])
    timeout = get_flag(FLAGS, "timeout_sec", 600)
    _print_workload_banner(banner_title, xid, expected_title_pattern, queue, workers, timeout)
    ep = get_flag(FLAGS, "endpoint")
    with handle_verification_errors(), ess_channel_context(ep) as (stub, _):
        try:
            exp = assert_experiment(stub, xid, expected_title_pattern)
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
    print_banner(
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
    print_success_summary(
        summary_title,
        [
            ("Dashboard ID", dashboard_id),
            ("Dashboard Title", dash_info["title"]),
            ("Validated Charts", len(dash_info.get("charts", []))),
            ("Web UI Link", f"{resolve_ui_base_url()}/dashboards/{dashboard_id}"),
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
    validate_argv(argv)
    raw_id = get_flag(FLAGS, "dashboard_id", None) or get_flag(FLAGS, "xid", None)
    parsed_id = parse_xid(raw_id, required=False) or default_dashboard_id
    dashboard_id = f"{default_dashboard_id}_{parsed_id}" if parsed_id.isdigit() else parsed_id
    title_pattern = get_flag(FLAGS, "expected_title_pattern", expected_title_pattern)
    timeout_sec = get_flag(FLAGS, "timeout_sec", default_timeout_sec)
    raw_ep = (
        get_flag(FLAGS, "dashboard_endpoint", None)
        or os.environ.get("XMC_DASHBOARD_REST_ENDPOINT")
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dash_ep = resolve_dashboard_endpoint(raw_ep)
    _print_dashboard_banner(banner_title, dashboard_id, title_pattern, dash_ep, timeout_sec)
    with handle_verification_errors():
        dash_info = assert_dashboard(
            dash_ep, dashboard_id, title_pattern, expected_min_charts, timeout_sec
        )
        _print_dashboard_summary(summary_title, dashboard_id, dash_info)


def run_main(main_fn: Any) -> None:
    """Standard CLI entrypoint wrapper executing absl.app.run."""
    app.run(main_fn)
