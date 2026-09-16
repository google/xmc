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

"""Generic validator library for XMC experiments, WorkUnits, and Dashboards.

Provides reusable assertions, gRPC client establishment, JobSet validation,
artifact assertions, dashboard REST API verification, and polling utilities
for validating XMC experiment and workload execution states.
"""

from __future__ import annotations

import json
import logging
import os
import re
import time
import urllib.error
import urllib.request
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any

try:
    import dotenv

    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
except ImportError:
    dotenv = None

# Graceful fallback: xmanager_cloud is an external wheel installed at runtime
# via install_xmanager.py. Allowing ImportError enables Bazel unit tests to run
# hermetically using mocks without requiring external wheel installation.
try:
    from xmanager_cloud.experiment_state_server import experiment_state_api as ess_api
    from xmanager_cloud.experiment_state_server.proto import api_pb2 as ess_api_pb2
except ImportError:
    ess_api = None
    ess_api_pb2 = None

# State enum names and integer constants matching cloud.xmanager.v1.WorkUnitStatus.WorkUnitState
STATE_NAMES = [
    "UNSPECIFIED",
    "INITIALIZING",
    "SCHEDULING",
    "STARTING",
    "RUNNING",
    "PARTIALLY_FAILED",
    "STOPPING",
    "RESTARTING",
    "STOPPED",
    "DESCHEDULING",
    "COMPLETED",
    "FAILED",
]
(
    STATE_UNSPECIFIED,
    STATE_INITIALIZING,
    STATE_SCHEDULING,
    STATE_STARTING,
    STATE_RUNNING,
    STATE_PARTIALLY_FAILED,
    STATE_STOPPING,
    STATE_RESTARTING,
    STATE_STOPPED,
    STATE_DESCHEDULING,
    STATE_COMPLETED,
    STATE_FAILED,
) = range(len(STATE_NAMES))


class TerminalFailureError(AssertionError):
    """Raised when a work unit or artifact enters a terminal failure state."""


def create_authenticated_stub(endpoint: str) -> tuple[Any, Any]:
    """Creates an authenticated ExperimentStateService stub and gRPC channel.

    Connects to the Experiment State Server (ESS) endpoint using native
    xmanager_cloud client authentication.

    Args:
        endpoint: gRPC endpoint host:port.

    Returns:
        A tuple of (stub, channel).

    Raises:
        RuntimeError: If xmanager_cloud is not available.
    """
    if ess_api is None:
        raise RuntimeError("xmanager_cloud is required to establish ESS gRPC connection.")

    return ess_api._create_experiment_state_server_stub(endpoint)


def _make_req(cls_name: str, **kwargs: Any) -> Any:
    """Instantiates a protobuf request message or duck-typed fallback."""
    if ess_api_pb2 is not None and hasattr(ess_api_pb2, cls_name):
        return getattr(ess_api_pb2, cls_name)(**kwargs)
    return SimpleNamespace(**kwargs)


def state_enum_to_name(state_enum: int) -> str:
    """Maps WorkUnitState enum value to a human-readable string."""
    if 0 <= state_enum < len(STATE_NAMES):
        return STATE_NAMES[state_enum]
    return f"UNKNOWN({state_enum})"


def extract_worker_name(work_unit: Any) -> str:
    """Extracts the primary worker name from a WorkUnit message.

    Reads the canonical executable job name in the JobSet specification.
    """
    try:
        if (
            hasattr(work_unit, "executable")
            and work_unit.executable.HasField("jobset")
            and work_unit.executable.jobset.jobs
            and work_unit.executable.jobset.jobs[0].name
        ):
            return work_unit.executable.jobset.jobs[0].name
    except Exception:
        pass

    return "unknown"


def extract_queue_name(work_unit: Any) -> str:
    """Extracts the target queue name from a WorkUnit message.

    Reads the canonical scheduling constraints queue name.
    """
    try:
        if (
            hasattr(work_unit, "executable")
            and work_unit.executable.HasField("scheduling_constraints")
            and work_unit.executable.scheduling_constraints.queue_name
        ):
            return work_unit.executable.scheduling_constraints.queue_name
    except Exception:
        pass

    return "unknown"


def _get_cached_wu_state(work_unit: Any) -> tuple[int, bool]:
    """Extracts cached WorkUnitState from current_work_unit_execution if present."""
    if not hasattr(work_unit, "current_work_unit_execution"):
        return STATE_UNSPECIFIED, False
    execution = work_unit.current_work_unit_execution
    if execution is None or not hasattr(execution, "HasField") or not execution.HasField("status"):
        return STATE_UNSPECIFIED, False
    raw_state = getattr(execution.status, "state", None)
    if raw_state is None:
        return STATE_UNSPECIFIED, False
    try:
        return int(raw_state), True
    except (TypeError, ValueError):
        return STATE_UNSPECIFIED, False


def _fetch_remote_wu_state(stub: Any, wu_name: str) -> int | None:
    """Queries ESS stub for the latest WorkUnitExecution state."""
    if stub is None or wu_name == "unknown":
        return None
    try:
        req = _make_req("GetCurrentWorkUnitExecutionRequest", name=wu_name)
        cur_exec = stub.GetCurrentWorkUnitExecution(req)
        if cur_exec and hasattr(cur_exec, "status"):
            state = getattr(cur_exec.status, "state", None)
            if state is not None:
                return int(state)
    except Exception as e:
        logging.warning(
            "Failed to retrieve current WorkUnitExecution for %s: %s",
            wu_name,
            e,
            exc_info=True,
        )
    return None


def extract_work_unit_state(stub: Any, work_unit: Any) -> int:
    """Retrieves current WorkUnitState enum integer for a WorkUnit."""
    cached_state, has_cached = _get_cached_wu_state(work_unit)
    terminal_cached = (
        STATE_UNSPECIFIED,
        STATE_COMPLETED,
        STATE_FAILED,
        STATE_PARTIALLY_FAILED,
    )
    if has_cached and cached_state in terminal_cached:
        return cached_state

    wu_name = getattr(work_unit, "name", "unknown")
    remote_state = _fetch_remote_wu_state(stub, wu_name)
    if remote_state is not None:
        return remote_state

    return cached_state


def validate_experiment(stub: Any, xid: str, expected_title_pattern: str | None = None) -> Any:
    """Fetches and verifies experiment existence and title matching."""
    exp_name = f"experiments/{xid}"
    req = _make_req("GetExperimentRequest", name=exp_name)
    experiment = stub.GetExperiment(req)
    if not experiment or not getattr(experiment, "name", ""):
        raise AssertionError(f"Experiment {exp_name} could not be retrieved from ESS.")

    exp_title = getattr(experiment, "title", "")
    if expected_title_pattern and expected_title_pattern not in exp_title:
        raise AssertionError(
            f"Experiment title mismatch: expected pattern '{expected_title_pattern}' "
            f"not found in title '{exp_title}'."
        )

    return experiment


def _evaluate_single_wu(stub: Any, wu: Any, expected_queue: str | None = None) -> dict[str, Any]:
    """Extracts metadata and verifies queue constraint for a single WorkUnit."""
    wu_name = getattr(wu, "name", "")
    wu_id = wu_name.split("/")[-1]
    worker_name = extract_worker_name(wu)
    queue_name = extract_queue_name(wu)

    if expected_queue is not None and queue_name != expected_queue:
        raise AssertionError(
            f"WorkUnit {wu_name} targeted queue '{queue_name}', expected '{expected_queue}'."
        )

    state_int = extract_work_unit_state(stub, wu)
    return {
        "id": wu_id,
        "name": wu_name,
        "worker_name": worker_name,
        "queue": queue_name,
        "state_enum": state_int,
        "state": state_enum_to_name(state_int),
    }


def _check_wu_expected_workers(
    found_workers: Sequence[str], expected_workers: Sequence[str] | None
) -> None:
    """Verifies all expected worker names are present in found workers."""
    if expected_workers is None:
        return
    missing = set(expected_workers) - set(found_workers)
    if missing:
        raise AssertionError(
            f"Expected worker names {list(expected_workers)} not satisfied. "
            f"Missing: {sorted(missing)}, Found: {sorted(found_workers)}."
        )


def _evaluate_work_units(
    stub: Any,
    work_units: Sequence[Any],
    expected_count: int | None = None,
    expected_queue: str | None = None,
    expected_worker_names: Sequence[str] | None = None,
) -> tuple[list[dict[str, Any]], bool]:
    """Evaluates a snapshot of work units, validating metadata and completion state."""
    if expected_count is not None and len(work_units) != expected_count:
        raise AssertionError(
            f"WorkUnit count mismatch: expected {expected_count}, found {len(work_units)}."
        )

    wu_results = [_evaluate_single_wu(stub, wu, expected_queue) for wu in work_units]
    found_workers = [r["worker_name"] for r in wu_results]
    _check_wu_expected_workers(found_workers, expected_worker_names)

    failed_units = [
        r for r in wu_results if r["state_enum"] in (STATE_FAILED, STATE_PARTIALLY_FAILED)
    ]
    if failed_units:
        raise TerminalFailureError(
            f"One or more WorkUnits failed execution: {json.dumps(failed_units, indent=2)}"
        )

    all_completed = all(r["state_enum"] == STATE_COMPLETED for r in wu_results)
    return wu_results, all_completed


def _fetch_raw_work_units(
    stub: Any, parent_name: str, elapsed: float, timeout_sec: int
) -> list[Any] | None:
    """Fetches raw work units from ESS stub, handling transient errors."""
    try:
        req = _make_req("ListWorkUnitsRequest", parent=parent_name)
        response = stub.ListWorkUnits(req)
        return list(getattr(response, "work_units", []))
    except Exception as e:
        if elapsed >= timeout_sec:
            raise
        logging.warning("Transient error fetching work units: %s. Retrying...", e)
        return None


def _check_wu_count_threshold(
    actual_count: int,
    min_expected: int,
    expected_count: int | None,
    parent_name: str,
    elapsed: float,
    timeout_sec: int,
) -> bool:
    """Validates work unit count bounds and returns True if minimum count is reached."""
    if actual_count < min_expected:
        if elapsed >= timeout_sec:
            raise AssertionError(
                f"Expected at least {min_expected} WorkUnits for {parent_name}, "
                f"but only found {actual_count} after {int(elapsed)}s."
            )
        return False
    if expected_count is not None and actual_count > expected_count:
        raise AssertionError(
            f"WorkUnit count exceeded: expected {expected_count}, found {actual_count}."
        )
    return True


def _log_wu_polling_progress(
    current_states: dict[str, str],
    last_states: dict[str, str],
    last_time: float,
    elapsed: float,
) -> tuple[dict[str, str], float]:
    """Logs state transitions or periodic progress if states changed or 30s elapsed."""
    if current_states != last_states or (elapsed - last_time >= 30.0):
        print(f"[{int(elapsed)}s] WorkUnit States: {current_states}", flush=True)
        return current_states, elapsed
    return last_states, last_time


def _poll_work_units_step(
    stub: Any,
    parent: str,
    min_exp: int,
    exp_count: int | None,
    exp_queue: str | None,
    workers: list[str] | None,
    elapsed: float,
    timeout: int,
) -> tuple[list[dict[str, Any]], bool] | None:
    """Fetches, asserts bounds, and evaluates work units for a single poll step."""
    work_units = _fetch_raw_work_units(stub, parent, elapsed, timeout)
    if work_units is None or not _check_wu_count_threshold(
        len(work_units), min_exp, exp_count, parent, elapsed, timeout
    ):
        return None
    return _evaluate_work_units(stub, work_units, exp_count, exp_queue, workers)


def _handle_wu_poll_iteration(
    res: tuple[list[dict[str, Any]], bool] | None,
    last_states: dict[str, str],
    last_time: float,
    elapsed: float,
    wait_for_completion: bool,
    timeout_sec: int,
) -> tuple[list[dict[str, Any]] | None, dict[str, str], float]:
    """Processes poll step results and returns validated records if completion conditions met."""
    if res is None:
        return None, last_states, last_time
    wu_results, all_completed = res
    current_states = {r["worker_name"]: r["state"] for r in wu_results}
    last_states, last_time = _log_wu_polling_progress(
        current_states, last_states, last_time, elapsed
    )
    if not wait_for_completion or all_completed:
        return wu_results, last_states, last_time
    if elapsed >= timeout_sec:
        raise TimeoutError(
            f"WorkUnits did not reach COMPLETED state within {timeout_sec}s timeout."
        )
    return None, last_states, last_time


def validate_work_units(
    stub: Any,
    xid: str,
    expected_count: int | None = None,
    expected_queue: str | None = None,
    expected_worker_names: Sequence[str] | None = None,
    timeout_sec: int = 600,
    poll_interval_sec: int = 5,
    wait_for_completion: bool = True,
) -> list[dict[str, Any]]:
    """Fetches, validates, and polls work units until all assertions pass."""
    parent = f"experiments/{xid}"
    workers = list(expected_worker_names) if expected_worker_names is not None else None
    min_exp = expected_count if expected_count is not None else (len(workers) if workers else 0)
    start_time, last_states, last_time = time.monotonic(), {}, -1.0

    while True:
        elapsed = time.monotonic() - start_time
        res = _poll_work_units_step(
            stub, parent, min_exp, expected_count, expected_queue, workers, elapsed, timeout_sec
        )
        results, last_states, last_time = _handle_wu_poll_iteration(
            res, last_states, last_time, elapsed, wait_for_completion, timeout_sec
        )
        if results is not None:
            return results
        time.sleep(poll_interval_sec)


def _extract_jobset_jobs(work_unit: Any) -> list[Any]:
    """Validates and extracts job objects from WorkUnit JobSet executable."""
    wu_name = getattr(work_unit, "name", "unknown")
    if not hasattr(work_unit, "executable"):
        raise AssertionError(f"WorkUnit '{wu_name}' missing executable field.")
    executable = work_unit.executable
    if hasattr(executable, "HasField") and not executable.HasField("jobset"):
        raise AssertionError(f"WorkUnit '{wu_name}' does not contain a JobSet executable.")
    jobset = getattr(executable, "jobset", None)
    if jobset is None or not hasattr(jobset, "jobs"):
        raise AssertionError(f"WorkUnit '{wu_name}' has invalid or empty JobSet.")
    return list(jobset.jobs)


def _validate_job_replicas(actual_job: Any, expected_job: dict[str, Any], job_name: str) -> None:
    """Validates job replica count matches expectation."""
    if "replicas" not in expected_job:
        return
    expected_replicas = int(expected_job["replicas"])
    actual_replicas = getattr(actual_job, "replicas", 1)
    if actual_replicas != expected_replicas:
        raise AssertionError(
            f"Job '{job_name}' replica count mismatch: "
            f"expected {expected_replicas}, found {actual_replicas}."
        )


def _validate_container_entry(
    actual_c: Any, expected_c: str | dict[str, Any], job_name: str, item_type: str
) -> None:
    """Validates a single container against string name or dictionary properties."""
    if isinstance(expected_c, str):
        act_name = getattr(actual_c, "name", "")
        if act_name != expected_c:
            raise AssertionError(
                f"Job '{job_name}' {item_type} container name mismatch: "
                f"expected '{expected_c}', found '{act_name}'."
            )
    elif isinstance(expected_c, dict):
        for k, v in expected_c.items():
            act_val = getattr(actual_c, k, None)
            if act_val != v:
                raise AssertionError(
                    f"Job '{job_name}' {item_type} container property '{k}' mismatch: "
                    f"expected '{v}', found '{act_val}'."
                )


def _validate_container_items(
    actual_items: Sequence[Any],
    expected_items: Sequence[str | dict[str, Any]] | int | None,
    job_name: str,
    item_type: str,
) -> None:
    """Validates count and properties for init or sidecar containers."""
    if expected_items is None:
        return
    if isinstance(expected_items, int):
        if len(actual_items) != expected_items:
            raise AssertionError(
                f"Job '{job_name}' {item_type} container count mismatch: "
                f"expected {expected_items}, found {len(actual_items)}."
            )
        return
    if len(actual_items) != len(expected_items):
        raise AssertionError(
            f"Job '{job_name}' {item_type} container count mismatch: "
            f"expected {len(expected_items)}, found {len(actual_items)}."
        )
    for exp_c, act_c in zip(expected_items, actual_items, strict=False):
        _validate_container_entry(act_c, exp_c, job_name, item_type)


def _validate_primary_spec(actual_job: Any, expected_job: dict[str, Any], job_name: str) -> None:
    """Validates primary container spec specification."""
    expected_spec = expected_job.get("spec") or expected_job.get("primary_container")
    if expected_spec is None:
        return
    actual_spec = getattr(actual_job, "spec", None)
    if actual_spec is None:
        raise AssertionError(f"Job '{job_name}' missing primary container spec.")
    if isinstance(expected_spec, str):
        act_name = getattr(actual_spec, "name", "")
        if act_name != expected_spec:
            raise AssertionError(
                f"Job '{job_name}' primary container name mismatch: "
                f"expected '{expected_spec}', found '{act_name}'."
            )
    elif isinstance(expected_spec, dict):
        for k, v in expected_spec.items():
            act_val = getattr(actual_spec, k, None)
            if act_val != v:
                raise AssertionError(
                    f"Job '{job_name}' primary container property '{k}' mismatch: "
                    f"expected '{v}', found '{act_val}'."
                )


def _build_job_summary(actual_job: Any) -> dict[str, Any]:
    """Constructs result dictionary summarizing validated JobSet job."""
    actual_inits = list(getattr(actual_job, "init_containers", []))
    actual_sidecars = list(getattr(actual_job, "sidecars", []))
    actual_spec = getattr(actual_job, "spec", None)
    return {
        "name": getattr(actual_job, "name", ""),
        "replicas": getattr(actual_job, "replicas", 1),
        "init_containers": [
            {
                "name": getattr(c, "name", ""),
                "docker_image": getattr(c, "docker_image", ""),
            }
            for c in actual_inits
        ],
        "sidecars": [
            {
                "name": getattr(s, "name", ""),
                "docker_image": getattr(s, "docker_image", ""),
            }
            for s in actual_sidecars
        ],
        "primary_container": getattr(actual_spec, "name", "") if actual_spec else "",
    }


def _validate_single_job(
    actual_job: Any, expected_job: dict[str, Any], job_name: str
) -> dict[str, Any]:
    """Validates replicas, inits, sidecars, and primary spec for a single job."""
    _validate_job_replicas(actual_job, expected_job, job_name)
    _validate_container_items(
        list(getattr(actual_job, "init_containers", [])),
        expected_job.get("init_containers"),
        job_name,
        "init",
    )
    _validate_container_items(
        list(getattr(actual_job, "sidecars", [])),
        expected_job.get("sidecars"),
        job_name,
        "sidecar",
    )
    _validate_primary_spec(actual_job, expected_job, job_name)
    return _build_job_summary(actual_job)


def validate_jobsets(
    work_unit: Any,
    expected_jobs: Sequence[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Validates constituent JobSet jobs, replica counts, init containers, and sidecars."""
    actual_jobs = _extract_jobset_jobs(work_unit)
    if len(actual_jobs) != len(expected_jobs):
        raise AssertionError(
            f"JobSet job count mismatch: expected {len(expected_jobs)}, found {len(actual_jobs)}."
        )

    actual_by_name = {getattr(j, "name", ""): j for j in actual_jobs}
    validated: list[dict[str, Any]] = []

    for idx, exp_job in enumerate(expected_jobs):
        exp_name = exp_job.get("name")
        if not exp_name:
            raise AssertionError(f"Expected job at index {idx} must specify a 'name'.")
        act_job = actual_by_name.get(exp_name) or (
            actual_jobs[idx] if idx < len(actual_jobs) else None
        )
        if act_job is None:
            raise AssertionError(
                f"Expected job '{exp_name}' not found in JobSet jobs: "
                f"{[getattr(j, 'name', '') for j in actual_jobs]}."
            )
        validated.append(_validate_single_job(act_job, exp_job, exp_name))

    return validated


def _normalize_artifact_type(type_name: str) -> str:
    """Normalizes artifact type string for uniform comparison."""
    name = type_name.strip()
    if name.startswith("ArtifactPayload."):
        name = name.split(".", 1)[1]
    s1 = re.sub(r"(.)([A-Z][a-z]+)", r"\1_\2", name)
    snake = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", s1).lower()
    if snake.endswith("_artifact") and len(snake) > len("_artifact"):
        snake = snake[: -len("_artifact")]
    elif snake.endswith("artifact") and len(snake) > len("artifact"):
        snake = snake[: -len("artifact")]
    return snake


def _resolve_artifact_parent(xid: str | int, wid: str | int | None = None) -> str:
    """Formats canonical parent resource name for Experiment or WorkUnit artifacts."""
    str_xid = str(xid).strip()
    prefix = str_xid if str_xid.startswith("experiments/") else f"experiments/{str_xid}"
    if wid is not None:
        return f"{prefix}/workUnits/{wid}"
    return prefix


def _fetch_raw_artifacts(
    stub: Any, parent_name: str, elapsed: float, timeout_sec: int
) -> list[Any] | None:
    """Fetches artifacts from ESS stub, retrying on transient errors."""
    try:
        req = _make_req("ListArtifactsRequest", parent=parent_name)
        response = stub.ListArtifacts(req)
        return list(getattr(response, "artifacts", []))
    except Exception as e:
        if elapsed >= timeout_sec:
            raise
        logging.warning("Transient error fetching artifacts for %s: %s", parent_name, e)
        return None


def _extract_artifact_payload_info(art: Any) -> tuple[str, str, Any]:
    """Extracts raw and normalized payload type from an artifact object."""
    payload = getattr(art, "payload", None)
    payload_type = ""
    if payload is not None:
        if hasattr(payload, "WhichOneof"):
            payload_type = payload.WhichOneof("type") or ""
        if not payload_type:
            for candidate in ("chart", "url", "managed_xprof", "code_source", "text"):
                if getattr(payload, candidate, None) is not None:
                    payload_type = candidate
                    break
    normalized = _normalize_artifact_type(payload_type) if payload_type else ""
    return payload_type, normalized, payload


def _extract_chart_artifact_details(art: Any, payload: Any) -> tuple[list[str], str]:
    """Extracts chart metric names and title, failing if chart provisioning failed."""
    chart = getattr(payload, "chart", None) if payload is not None else getattr(art, "chart", None)
    if chart is None:
        return [], ""
    status = getattr(chart, "status", None)
    if status in (3, "FAILED", "STATUS_FAILED"):
        err = getattr(chart, "error_message", "")
        art_name = getattr(art, "name", "")
        raise TerminalFailureError(f"ChartArtifact '{art_name}' failed provisioning: {err}")
    metric_names = list(getattr(chart, "metric_names", []))
    title = getattr(chart, "title", "")
    return metric_names, title


def _extract_url_artifact_details(art: Any, payload: Any) -> tuple[str, str]:
    """Extracts URL string and display name from URL artifact payload."""
    url_obj = getattr(payload, "url", None) if payload is not None else getattr(art, "url", None)
    if url_obj is None:
        return "", ""
    url_str = getattr(url_obj, "url", "") if hasattr(url_obj, "url") else str(url_obj)
    display_name = getattr(url_obj, "display_name", "") if hasattr(url_obj, "display_name") else ""
    return url_str, display_name


def _process_single_artifact(art: Any) -> dict[str, Any]:
    """Processes a single artifact object into normalized metadata dictionary."""
    payload_type, norm_type, payload = _extract_artifact_payload_info(art)
    metrics, chart_title = _extract_chart_artifact_details(art, payload)
    url_str, display_name = _extract_url_artifact_details(art, payload)
    title = chart_title or display_name or getattr(art, "notes", "")
    return {
        "name": getattr(art, "name", ""),
        "parent": getattr(art, "parent", ""),
        "type": payload_type or "unknown",
        "normalized_type": norm_type or "unknown",
        "metric_names": metrics,
        "title": title,
        "url": url_str if isinstance(url_str, str) else getattr(url_str, "url", ""),
    }


def _raise_artifact_timeout(
    types_ok: bool,
    metrics_ok: bool,
    exp_types: set[str] | None,
    exp_metrics: set[str] | None,
    found_types: set[str],
    found_metrics: set[str],
    parent_name: str,
    timeout_sec: int,
) -> None:
    """Raises descriptive AssertionError or TimeoutError on artifact polling expiration."""
    if not types_ok and exp_types:
        missing = sorted(exp_types - found_types)
        raise AssertionError(
            f"Expected artifact types {missing} not found in {parent_name}. "
            f"Found types: {sorted(found_types)}."
        )
    if not metrics_ok and exp_metrics:
        missing = sorted(exp_metrics - found_metrics)
        raise AssertionError(
            f"Expected metric names {missing} not found in ChartArtifacts for {parent_name}. "
            f"Found metrics: {sorted(found_metrics)}."
        )
    raise TimeoutError(f"Artifact verification timed out after {timeout_sec}s.")


def _check_artifact_satisfaction(
    found_types: set[str],
    found_metrics: set[str],
    exp_types: set[str] | None,
    exp_metrics: set[str] | None,
    parent_name: str,
    has_raw: bool,
    elapsed: float,
    timeout_sec: int,
) -> bool:
    """Verifies expected artifact types and metrics satisfaction or raises on timeout."""
    types_ok = exp_types.issubset(found_types) if exp_types is not None else True
    metrics_ok = exp_metrics.issubset(found_metrics) if exp_metrics is not None else True
    if types_ok and metrics_ok and (has_raw or not (exp_types or exp_metrics)):
        return True
    if elapsed >= timeout_sec:
        _raise_artifact_timeout(
            types_ok,
            metrics_ok,
            exp_types,
            exp_metrics,
            found_types,
            found_metrics,
            parent_name,
            timeout_sec,
        )
    return False


def _poll_artifacts_step(
    stub: Any,
    parent_name: str,
    exp_types: set[str] | None,
    exp_metrics: set[str] | None,
    elapsed: float,
    timeout_sec: int,
) -> list[dict[str, Any]] | None:
    """Fetches raw artifacts and checks satisfaction for a single poll step."""
    raw = _fetch_raw_artifacts(stub, parent_name, elapsed, timeout_sec)
    if raw is None:
        return None
    results = [_process_single_artifact(art) for art in raw]
    found_types = {r["normalized_type"] for r in results if r["normalized_type"] != "unknown"}
    found_metrics = {m for r in results for m in r["metric_names"]}
    if _check_artifact_satisfaction(
        found_types,
        found_metrics,
        exp_types,
        exp_metrics,
        parent_name,
        bool(raw),
        elapsed,
        timeout_sec,
    ):
        return results
    return None


def validate_artifacts(
    stub: Any,
    xid: str | int,
    wid: str | int | None = None,
    expected_artifact_types: Sequence[str] | None = None,
    expected_metric_names: Sequence[str] | None = None,
    timeout_sec: int = 60,
    poll_interval_sec: int = 2,
) -> list[dict[str, Any]]:
    """Fetches and validates artifacts associated with an Experiment or WorkUnit."""
    parent = _resolve_artifact_parent(xid, wid)
    exp_types = (
        {_normalize_artifact_type(t) for t in expected_artifact_types}
        if expected_artifact_types is not None
        else None
    )
    exp_metrics = set(expected_metric_names) if expected_metric_names is not None else None
    start_time = time.monotonic()

    while True:
        elapsed = time.monotonic() - start_time
        results = _poll_artifacts_step(stub, parent, exp_types, exp_metrics, elapsed, timeout_sec)
        if results is not None:
            return results
        time.sleep(poll_interval_sec)


def _normalize_expected_dashboards(
    expected_dashboards: Sequence[dict[str, Any] | str],
) -> list[dict[str, Any]]:
    """Normalizes expected dashboard specifications to standard dictionary format."""
    normalized: list[dict[str, Any]] = []
    for item in expected_dashboards:
        if isinstance(item, str):
            normalized.append({"dashboard_id": item})
        else:
            normalized.append(dict(item))
    return normalized


def _fetch_json_from_url(url: str, timeout: int = 10) -> dict[str, Any]:
    """Performs HTTP GET request with standard headers and parses JSON response."""
    req = urllib.request.Request(
        url,
        headers={"Accept": "application/json", "User-Agent": "xmc-validator/1.0"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _validate_dashboard_charts(base_endpoint: str, chart_ids: list[str]) -> list[dict[str, Any]]:
    """Fetches and verifies individual chart definitions referenced by a dashboard."""
    verified_charts: list[dict[str, Any]] = []
    for cid in chart_ids:
        chart_slug = cid.split("/", 1)[1] if cid.startswith("charts/") else cid
        chart_url = f"{base_endpoint}/v1/charts/{chart_slug}"
        verified_charts.append(_fetch_json_from_url(chart_url))
    return verified_charts


def _extract_dashboard_id(exp_dash: dict[str, Any]) -> str:
    """Extracts and normalizes the target dashboard ID."""
    raw_id = exp_dash.get("dashboard_id") or exp_dash.get("id") or exp_dash.get("name", "")
    dash_id = raw_id.split("/", 1)[1] if raw_id.startswith("dashboards/") else raw_id
    if not dash_id:
        raise AssertionError("Expected dashboard specification missing dashboard_id.")
    return dash_id


def _assert_dashboard_metadata(
    dash_id: str, dash_data: dict[str, Any], exp_dash: dict[str, Any]
) -> list[str]:
    """Asserts title pattern and expected chart IDs for fetched dashboard data."""
    dash_title = dash_data.get("title", "")
    if "expected_title" in exp_dash and exp_dash["expected_title"] not in dash_title:
        raise AssertionError(
            f"Dashboard '{dash_id}' title mismatch: "
            f"expected '{exp_dash['expected_title']}', found '{dash_title}'."
        )
    chart_ids = dash_data.get("chart_ids") or dash_data.get("chartIds") or []
    if "expected_chart_ids" in exp_dash:
        missing = set(exp_dash["expected_chart_ids"]) - set(chart_ids)
        if missing:
            raise AssertionError(
                f"Dashboard '{dash_id}' missing expected chart IDs: {sorted(missing)}. "
                f"Found: {sorted(chart_ids)}."
            )
    return chart_ids


def _validate_single_dashboard(base_endpoint: str, exp_dash: dict[str, Any]) -> dict[str, Any]:
    """Fetches a dashboard by ID, asserts constraints, and validates its charts."""
    dash_id = _extract_dashboard_id(exp_dash)
    dash_data = _fetch_json_from_url(f"{base_endpoint}/v1/dashboards/{dash_id}")
    chart_ids = _assert_dashboard_metadata(dash_id, dash_data, exp_dash)
    charts = _validate_dashboard_charts(base_endpoint, chart_ids)
    return {
        "dashboard_id": dash_id,
        "name": dash_data.get("name", f"dashboards/{dash_id}"),
        "title": dash_data.get("title", ""),
        "chart_ids": chart_ids,
        "charts": charts,
    }


def _normalize_dashboard_endpoint(endpoint: str) -> str:
    """Ensures dashboard endpoint has http/https protocol prefix."""
    base = endpoint.strip().rstrip("/")
    return base if base.startswith(("http://", "https://")) else f"http://{base}"


def _poll_dashboard_iteration(
    base_endpoint: str,
    normalized: list[dict[str, Any]],
    elapsed: float,
    timeout_sec: int,
) -> list[dict[str, Any]] | None:
    """Attempts to validate all dashboards in a single polling pass."""
    try:
        return [_validate_single_dashboard(base_endpoint, exp) for exp in normalized]
    except (urllib.error.URLError, ConnectionError, OSError) as e:
        if elapsed >= timeout_sec:
            raise TimeoutError(
                f"Dashboard REST endpoint verification failed after {timeout_sec}s: {e}"
            )
        logging.warning("Transient network error reaching dashboard: %s. Retrying...", e)
        return None
    except AssertionError:
        if elapsed >= timeout_sec:
            raise
        return None


def validate_dashboard(
    dashboard_endpoint: str,
    expected_dashboards: Sequence[dict[str, Any] | str],
    timeout_sec: int = 60,
    poll_interval_sec: int = 2,
) -> list[dict[str, Any]]:
    """Validates Dashboards and Charts via Dashboard Service REST API endpoints."""
    base_endpoint = _normalize_dashboard_endpoint(dashboard_endpoint)
    normalized = _normalize_expected_dashboards(expected_dashboards)
    start_time = time.monotonic()

    while True:
        elapsed = time.monotonic() - start_time
        res = _poll_dashboard_iteration(base_endpoint, normalized, elapsed, timeout_sec)
        if res is not None:
            return res
        time.sleep(poll_interval_sec)


def resolve_ui_base_url() -> str:
    """Resolves the XMC UI origin URL from environment variables."""
    if ui_url := os.environ.get("XMANAGER_UI_URL", "").strip():
        return ui_url.rstrip("/")
    domain = os.environ.get("GCP_DOMAIN_NAME", "localhost:8080").strip().rstrip("/")
    if domain.startswith(("http://", "https://")):
        return domain
    if domain.startswith(("localhost", "127.0.0.1")):
        return f"http://{domain}"
    return f"https://{domain}"
