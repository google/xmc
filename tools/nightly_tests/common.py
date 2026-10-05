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
endpoint resolution, argument validation, and formatted logging helpers
used across XMC verification CLI scripts.
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
    step_label: str = "1/3",
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
    step_label: str = "2/3",
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
    step_label: str = "3/3",
) -> list[dict[str, Any]]:
    """Fetches WorkUnits and validates JobSet topology and container definitions."""
    if not expected_jobs:
        return []
    print(f"\n[{step_label}] Validating JobSet specifications...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if not raw_work_units:
        raise AssertionError(f"No WorkUnits found for experiment {xid}.")
    job_results = xmc_entities_validator.validate_jobsets(
        work_unit=raw_work_units[0],
        expected_jobs=expected_jobs,
    )
    for job in job_results:
        print(
            f"  [✓] Job '{job['name']}': Replicas={job['replicas']}, "
            f"PrimaryContainer='{job['primary_container']}'"
        )
    return job_results


def assert_clickhouse_metrics(
    xid: str,
    expected_metric_names: Sequence[str] | set[str],
    expected_min_rows: int = 1,
    timeout_sec: int = 60,
    step_label: str = "4/5",
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
    strict_required: bool | None = None,
    step_label: str = "5/5",
    expected_chart_id: str | None = None,
) -> dict[str, Any]:
    """Verifies Dashboard metadata and GetChartData hydration via Dashboard Service."""
    raw = (
        get_flag(FLAGS, "dashboard_endpoint")
        or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dash_ep = resolve_dashboard_endpoint(raw)
    if strict_required is None:
        strict_required = bool(
            get_flag(FLAGS, "require_clickhouse_metrics", False)
            or raw
            or os.environ.get("XMANAGER_UI_URL")
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


def verify_standard_workload(
    argv: Sequence[str],
    banner_title: str,
    summary_title: str,
    expected_title_pattern: str,
    expected_jobs: Sequence[dict[str, Any]] | None = None,
    expected_worker_names: Sequence[str] | None = None,
    extra_step: Callable[[Any, str], None] | None = None,
) -> None:
    """Orchestrates standard Experiment, WorkUnit, and JobSet verification."""
    validate_argv(argv)
    xid, queue = parse_xid(get_flag(FLAGS, "xid")), get_flag(FLAGS, "expected_queue", "cpu-queue")
    workers = expected_worker_names or ([expected_jobs[0]["name"]] if expected_jobs else [])
    timeout, n_wus = get_flag(FLAGS, "timeout_sec", 600), resolve_expected_work_units(len(workers))
    endpoint = get_flag(FLAGS, "endpoint")
    print_banner(banner_title, {"XID": xid, "Title": expected_title_pattern, "Queue": queue})
    with handle_verification_errors(), ess_channel_context(endpoint) as (stub, _):
        exp = assert_experiment(stub, xid, expected_title_pattern, "1/3")
        assert_work_units(stub, xid, n_wus, queue, workers, timeout, "2/3")
        assert_work_unit_jobsets(stub, xid, expected_jobs, "3/3")
        if extra_step:
            extra_step(stub, xid)
        ui = f"{resolve_ui_base_url()}/experiments/{xid}"
        print_success_summary(summary_title, {"Experiment": exp.name, "Queue": queue, "UI": ui})


def run_main(main_fn: Any) -> None:
    """Standard CLI entrypoint wrapper executing absl.app.run."""
    app.run(main_fn)
