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

"""Generic validator library for XMC experiments and WorkUnits.

Provides reusable assertions, gRPC client establishment, and polling utilities
for validating XMC experiment and workload execution states.
"""

from __future__ import annotations

import json
import logging
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any

try:
    import dotenv

    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
except ImportError:
    dotenv: Any = None  # type: ignore[no-redef]

# Graceful fallback: xmanager_cloud is an external wheel installed at runtime
# via install_xmanager.py. Allowing ImportError enables Bazel unit tests to run
# hermetically using mocks without requiring external wheel installation.
try:
    from xmanager_cloud.experiment_state_server import experiment_state_api as ess_api
    from xmanager_cloud.experiment_state_server.proto import api_pb2 as ess_api_pb2, priority_pb2
except ImportError:
    ess_api = None
    ess_api_pb2 = None
    priority_pb2 = None

# State enum names matching cloud.xmanager.v1.WorkUnitStatus.WorkUnitState
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

# Priority fallback mapping matching cloud.xmanager.v1.Priority
PRIORITY_FALLBACK_MAP = {
    "PRIORITY_UNSPECIFIED": 0,
    "LOW": 1,
    "NORMAL": 2,
    "HIGH": 3,
}


class TerminalFailureError(AssertionError):
    """Raised when a work unit enters a terminal failure state."""


def create_authenticated_stub(endpoint: str) -> tuple[Any, Any]:
    """Creates an authenticated ExperimentStateService stub and gRPC channel."""
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
    """Extracts the primary worker name from a WorkUnit message."""
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
    """Extracts the target queue name from a WorkUnit message."""
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
    """Extracts WorkUnitState from current execution status if present."""
    if (
        hasattr(work_unit, "current_work_unit_execution")
        and work_unit.current_work_unit_execution.HasField("status")
        and getattr(work_unit.current_work_unit_execution.status, "state", None) is not None
    ):
        return int(work_unit.current_work_unit_execution.status.state), True
    return STATE_UNSPECIFIED, False


def _fetch_remote_wu_state(stub: Any, wu_name: str) -> int | None:
    """Fetches WorkUnitState directly from ESS RPC for a given WorkUnit."""
    if stub is None or wu_name == "unknown":
        return None
    try:
        req = _make_req("GetCurrentWorkUnitExecutionRequest", name=wu_name)
        cur_exec = stub.GetCurrentWorkUnitExecution(req)
        if (
            cur_exec
            and hasattr(cur_exec, "status")
            and getattr(cur_exec.status, "state", None) is not None
        ):
            return int(cur_exec.status.state)
    except Exception as e:
        logging.warning("Failed to fetch state for %s: %s", wu_name, e, exc_info=True)
    return None


def extract_work_unit_state(stub: Any, work_unit: Any) -> int:
    """Retrieves current WorkUnitState enum integer for a WorkUnit."""
    cached_state, found = _get_cached_wu_state(work_unit)
    if found:
        return cached_state
    wu_name = getattr(work_unit, "name", "unknown")
    remote_state = _fetch_remote_wu_state(stub, wu_name)
    return remote_state if remote_state is not None else STATE_UNSPECIFIED


def validate_experiment(
    stub: Any,
    xid: str,
    expected_title_pattern: str | None = None,
) -> Any:
    """Fetches and verifies experiment existence and title."""
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


def validate_work_unit_priority(
    work_unit: Any,
    expected_priority: int | str,
) -> None:
    """Validates that a WorkUnit has the expected priority enum or string."""
    wu_name = getattr(work_unit, "name", "unknown")
    actual_priority = getattr(work_unit, "priority", None)
    if actual_priority is None:
        raise AssertionError(f"WorkUnit '{wu_name}' missing priority field.")

    if isinstance(expected_priority, str):
        if priority_pb2 is not None and hasattr(priority_pb2.Priority, expected_priority.upper()):
            exp_val = getattr(priority_pb2.Priority, expected_priority.upper())
        else:
            exp_val = PRIORITY_FALLBACK_MAP.get(expected_priority.upper(), 0)
    else:
        exp_val = expected_priority

    if int(actual_priority) != int(exp_val):
        raise AssertionError(
            f"WorkUnit '{wu_name}' priority mismatch: expected {exp_val}, found {actual_priority}."
        )


def _validate_cpu_resources(
    cpu_res: Any,
    expected_cpu_milli: int | None,
    expected_memory_bytes: int | None,
) -> None:
    """Validates CPU millicores and memory bytes."""
    if expected_cpu_milli is not None:
        act_cpu = getattr(cpu_res, "cpu_millicores", 0) if cpu_res else 0
        if act_cpu != expected_cpu_milli:
            raise AssertionError(
                f"CPU millicores mismatch: expected {expected_cpu_milli}m, found {act_cpu}m."
            )
    if expected_memory_bytes is not None:
        act_mem = getattr(cpu_res, "memory_bytes", 0) if cpu_res else 0
        if act_mem != expected_memory_bytes:
            raise AssertionError(
                f"Memory bytes mismatch: expected {expected_memory_bytes}, found {act_mem}."
            )


def _validate_gpu_resources(gpu_res: Any, expected_gpus: int | None) -> None:
    """Validates GPU resource count."""
    if expected_gpus is not None:
        act_gpus = getattr(gpu_res, "count", 0) if gpu_res else 0
        if act_gpus != expected_gpus:
            raise AssertionError(f"GPU count mismatch: expected {expected_gpus}, found {act_gpus}.")


def validate_resource_requests(
    spec_or_job: Any,
    expected_cpu_milli: int | None = None,
    expected_memory_bytes: int | None = None,
    expected_gpus: int | None = None,
) -> None:
    """Validates CPU millicores, memory bytes, and GPU counts on an ExecutableSpec or Job."""
    spec = getattr(spec_or_job, "spec", spec_or_job)
    resources = getattr(spec, "resources", None)
    if resources is None:
        raise AssertionError("Missing resources specification on ExecutableSpec.")
    _validate_cpu_resources(
        getattr(resources, "cpu_resources", None),
        expected_cpu_milli,
        expected_memory_bytes,
    )
    _validate_gpu_resources(getattr(resources, "gpu_resources", None), expected_gpus)


def validate_work_unit_metadata(
    work_unit: Any,
    expected_tags: Sequence[str] | None = None,
    expected_notes_substring: str | None = None,
) -> None:
    """Validates metadata tags and notes on a WorkUnit."""
    wu_name = getattr(work_unit, "name", "unknown")
    if expected_notes_substring is not None:
        notes = getattr(work_unit, "notes", "")
        if expected_notes_substring not in notes:
            raise AssertionError(
                f"WorkUnit '{wu_name}' notes missing substring '{expected_notes_substring}': "
                f"found '{notes}'."
            )
    if expected_tags is not None:
        actual_tags = set(getattr(work_unit, "tags", []))
        missing_tags = set(expected_tags) - actual_tags
        if missing_tags:
            raise AssertionError(
                f"WorkUnit '{wu_name}' missing expected tags: {sorted(missing_tags)}. "
                f"Actual tags: {sorted(actual_tags)}."
            )


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
    """Validates multi-role JobSet topology and container definitions."""
    actual_jobs = _extract_jobset_jobs(work_unit)
    if len(actual_jobs) != len(expected_jobs):
        raise AssertionError(
            f"JobSet job count mismatch: expected {len(expected_jobs)}, found {len(actual_jobs)}."
        )

    actual_by_name = {getattr(j, "name", ""): j for j in actual_jobs}
    job_summaries: list[dict[str, Any]] = []

    for expected_job in expected_jobs:
        job_name = expected_job["name"]
        if job_name not in actual_by_name:
            raise AssertionError(f"Expected JobSet job '{job_name}' not found in WorkUnit.")
        summary = _validate_single_job(actual_by_name[job_name], expected_job, job_name)
        job_summaries.append(summary)

    return job_summaries


def validate_jobset_topology(
    work_unit: Any,
    expected_jobs: Sequence[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Alias for validate_jobsets for backwards compatibility."""
    return validate_jobsets(work_unit, expected_jobs)


def _evaluate_single_wu(stub: Any, wu: Any, expected_queue: str | None = None) -> dict[str, Any]:
    """Extracts metadata and evaluates state for a single WorkUnit."""
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
    _check_wu_expected_workers([r["worker_name"] for r in wu_results], expected_worker_names)
    failed_units = [
        r for r in wu_results if r["state_enum"] in (STATE_FAILED, STATE_PARTIALLY_FAILED)
    ]
    if failed_units:
        raise TerminalFailureError(
            f"One or more WorkUnits failed execution: {json.dumps(failed_units, indent=2)}"
        )
    return wu_results, all(r["state_enum"] == STATE_COMPLETED for r in wu_results)


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


def _run_wu_poll_loop(
    stub: Any,
    parent: str,
    min_exp: int,
    exp_count: int | None,
    exp_queue: str | None,
    workers: list[str] | None,
    timeout: int,
    interval: int,
    wait_for_completion: bool,
) -> list[dict[str, Any]]:
    """Runs polling loop for work units until completed or timeout."""
    start_time, last_time = time.monotonic(), -1.0
    last_states: dict[str, str] = {}
    while True:
        elapsed = time.monotonic() - start_time
        res = _poll_work_units_step(
            stub, parent, min_exp, exp_count, exp_queue, workers, elapsed, timeout
        )
        res_list, last_states, last_time = _handle_wu_poll_iteration(
            res, last_states, last_time, elapsed, wait_for_completion, timeout
        )
        if res_list is not None:
            return res_list
        time.sleep(interval)


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
    workers = list(expected_worker_names) if expected_worker_names is not None else None
    min_exp = expected_count if expected_count is not None else (len(workers) if workers else 0)
    return _run_wu_poll_loop(
        stub,
        f"experiments/{xid}",
        min_exp,
        expected_count,
        expected_queue,
        workers,
        timeout_sec,
        poll_interval_sec,
        wait_for_completion,
    )


def wait_for_experiment_state(
    stub: Any,
    xid: str,
    timeout_sec: int = 600,
    poll_interval_sec: int = 5,
) -> list[dict[str, Any]]:
    """Polls until all WorkUnits for an experiment reach COMPLETED state."""
    return validate_work_units(
        stub=stub,
        xid=xid,
        timeout_sec=timeout_sec,
        poll_interval_sec=poll_interval_sec,
        wait_for_completion=True,
    )


def _normalize_artifact_type(type_name: str) -> str:
    """Normalizes artifact type string (e.g., 'ChartArtifact' -> 'chart')."""
    base = type_name.strip().split(".")[-1]
    snake = re.sub(r"(?<!^)(?=[A-Z])", "_", base).lower()
    return snake.removeprefix("type_").removesuffix("artifact").strip("_")


def _fetch_raw_artifacts(
    stub: Any, parent_name: str, elapsed: float, timeout_sec: int
) -> list[Any] | None:
    """Fetches raw artifacts from ESS stub with transient error handling."""
    try:
        req = _make_req("ListArtifactsRequest", parent=parent_name)
        response = stub.ListArtifacts(req)
        return list(getattr(response, "artifacts", []))
    except Exception as e:
        if elapsed >= timeout_sec:
            raise
        logging.warning("Transient error fetching artifacts: %s. Retrying...", e)
        return None


def _extract_artifact_payload_info(art: Any) -> tuple[str, Any]:
    """Extracts normalized payload type and active subtype object from an Artifact."""
    payload = getattr(art, "payload", None)
    which = payload.WhichOneof("type") if hasattr(payload, "WhichOneof") else None
    if not which:
        return "", None
    return _normalize_artifact_type(which), getattr(payload, which, None)


def _extract_chart_artifact_details(art_name: str, chart: Any) -> tuple[list[str], str]:
    """Extracts metric names and title from ChartArtifact payload."""
    status = getattr(chart, "status", None)
    if status in (3, "FAILED"):
        err = getattr(chart, "error_message", "")
        raise TerminalFailureError(f"ChartArtifact '{art_name}' failed provisioning: {err}")
    if status in (1, "PROVISIONING"):
        return [], ""
    return list(getattr(chart, "metric_names", [])), getattr(chart, "title", "")


def _process_single_artifact(art: Any) -> dict[str, Any]:
    """Processes a single artifact object into normalized metadata dictionary."""
    art_name = getattr(art, "name", "")
    norm_type, payload = _extract_artifact_payload_info(art)
    satisfied_type = norm_type or "unknown"
    metrics: list[str] = []
    title, url_str = "", ""
    if norm_type == "chart" and payload is not None:
        if getattr(payload, "status", None) in (1, "PROVISIONING"):
            satisfied_type = "provisioning_chart"
        metrics, title = _extract_chart_artifact_details(art_name, payload)
    elif norm_type == "url" and payload is not None:
        url_str = getattr(payload, "url", "") or getattr(getattr(art, "url", None), "url", "")
        if not url_str.strip():
            raise AssertionError(f"Found URL artifact '{art_name}' with empty URL field.")
        title = getattr(payload, "display_name", "")
    return {
        "name": art_name,
        "parent": getattr(art, "parent", ""),
        "type": norm_type or "unknown",
        "normalized_type": satisfied_type,
        "metric_names": metrics,
        "title": title or getattr(art, "notes", ""),
        "url": url_str,
    }


def _verify_artifacts_satisfied(
    results: list[dict[str, Any]],
    exp_types: set[str],
    exp_metrics: set[str],
    parent_name: str,
    timed_out: bool,
) -> bool:
    """Verifies expected artifact types and metrics satisfaction or raises on timeout."""
    found_types = {r["normalized_type"] for r in results if r["normalized_type"] != "unknown"}
    found_metrics = {m for r in results for m in r["metric_names"]}
    missing_types = sorted(exp_types - found_types)
    missing_metrics = sorted(exp_metrics - found_metrics)
    if not missing_types and not missing_metrics:
        return True
    if not timed_out:
        return False
    if missing_types:
        raise AssertionError(
            f"Expected artifact types {missing_types} not found in {parent_name}. "
            f"Found types: {sorted(found_types)}."
        )
    raise AssertionError(
        f"Expected metric names {missing_metrics} not found in ChartArtifacts for {parent_name}. "
        f"Found metrics: {sorted(found_metrics)}."
    )


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
    parent = f"experiments/{xid}/workUnits/{wid}" if wid is not None else f"experiments/{xid}"
    exp_types = {_normalize_artifact_type(t) for t in (expected_artifact_types or ())}
    exp_metrics = set(expected_metric_names or ())
    start_time = time.monotonic()

    while True:
        elapsed = time.monotonic() - start_time
        raw = _fetch_raw_artifacts(stub, parent, elapsed, timeout_sec)
        if raw is not None:
            results = [_process_single_artifact(art) for art in raw]
            if _verify_artifacts_satisfied(
                results, exp_types, exp_metrics, parent, elapsed >= timeout_sec
            ):
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
    headers = {"Accept": "application/json", "User-Agent": "xmc-validator/1.0"}
    token = (os.environ.get("XMC_AUTH_TOKEN") or os.environ.get("AUTH_TOKEN") or "").strip()
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers)
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
        exp_cids = exp_dash["expected_chart_ids"]
        missing = set(exp_cids) - set(chart_ids)
        if missing:
            raise AssertionError(
                f"Dashboard '{dash_id}' missing expected chart IDs: {sorted(missing)}. "
                f"Found: {sorted(chart_ids)}."
            )
    return chart_ids


def _validate_single_dashboard(base_endpoint: str, exp_dash: dict[str, Any]) -> dict[str, Any]:
    """Fetches and validates metadata and charts for a single dashboard."""
    dash_id = _extract_dashboard_id(exp_dash)
    dash_url = f"{base_endpoint}/v1/dashboards/{dash_id}"
    dash_data = _fetch_json_from_url(dash_url)
    chart_ids = _assert_dashboard_metadata(dash_id, dash_data, exp_dash)
    verified_charts = _validate_dashboard_charts(base_endpoint, chart_ids)
    return {
        "id": dash_id,
        "dashboard_id": dash_id,
        "title": dash_data.get("title", ""),
        "chart_ids": chart_ids,
        "charts": verified_charts,
        "raw_response": dash_data,
    }


def _normalize_dashboard_endpoint(endpoint: str) -> str:
    """Ensures dashboard endpoint has valid scheme and no trailing slash."""
    ep = endpoint.strip().rstrip("/")
    if not ep.startswith(("http://", "https://")):
        return f"http://{ep}" if ep.startswith(("localhost", "127.0.0.1")) else f"https://{ep}"
    return ep


def _poll_dashboard_iteration(
    base_endpoint: str,
    norm_dashboards: list[dict[str, Any]],
    elapsed: float,
    timeout_sec: int,
) -> list[dict[str, Any]] | None:
    """Attempts to validate all dashboards for a single poll step."""
    try:
        return [_validate_single_dashboard(base_endpoint, d) for d in norm_dashboards]
    except AssertionError as e:
        if elapsed >= timeout_sec:
            raise
        logging.warning("Dashboard assertions not yet satisfied: %s. Retrying...", e)
        return None
    except (urllib.error.HTTPError, urllib.error.URLError, OSError, ValueError) as e:
        if elapsed >= timeout_sec:
            raise TimeoutError(f"Dashboard verification failed after {int(elapsed)}s: {e}") from e
        logging.warning("Transient error fetching dashboard: %s. Retrying...", e)
        return None


def validate_dashboard(
    endpoint: str = "",
    expected_dashboards: Sequence[dict[str, Any] | str] | None = None,
    timeout_sec: int = 60,
    poll_interval_sec: int = 2,
    dashboard_endpoint: str | None = None,
) -> list[dict[str, Any]]:
    """Fetches and validates dashboard definitions and referenced charts via REST API."""
    target_ep = dashboard_endpoint or endpoint
    if not target_ep:
        raise ValueError("endpoint or dashboard_endpoint must be specified.")
    if expected_dashboards is None:
        expected_dashboards = []
    base_endpoint = _normalize_dashboard_endpoint(target_ep)
    norm_dashboards = _normalize_expected_dashboards(expected_dashboards)
    start_time = time.monotonic()

    while True:
        elapsed = time.monotonic() - start_time
        res = _poll_dashboard_iteration(base_endpoint, norm_dashboards, elapsed, timeout_sec)
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


def _http_get(
    url: str,
    headers: dict[str, str] | None = None,
    timeout_sec: int = 10,
    service_name: str = "",
) -> Any:
    """Executes an HTTP GET request and returns decoded JSON or text."""
    prefix = f"{service_name} " if service_name else ""
    req = urllib.request.Request(url, headers=headers or {}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
            content = resp.read().decode("utf-8")
            if not content.strip():
                return None
            try:
                return json.loads(content)
            except json.JSONDecodeError:
                return content.strip()
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        if "data engine is not configured" in body.lower():
            raise AssertionError(
                f"Dashboard Service returned 'data engine is not configured' for {url}: {body}"
            ) from err
        raise RuntimeError(f"{prefix}HTTP request failed ({err.code}) for {url}: {body}") from err


def query_clickhouse_sql(
    endpoint: str,
    query: str,
    database: str = "default",
    timeout_sec: int = 10,
) -> Any:
    """Executes a SQL query against ClickHouse HTTP interface and returns parsed JSON."""
    params = {"query": query, "database": database}
    return _http_get(
        f"{endpoint}/?{urllib.parse.urlencode(params)}",
        timeout_sec=timeout_sec,
        service_name="ClickHouse",
    )


def _log_and_check_clickhouse_metrics(
    xid: str,
    total_rows: int,
    rows: list[dict[str, Any]],
    expected_metric_names: Sequence[str] | set[str] | None,
) -> None:
    """Logs ClickHouse metric summary and asserts expected metric names are present."""
    print(f"  [✓] ClickHouse Table 'xmc_metrics': {total_rows} total rows found for XID {xid}.")
    for r in rows:
        print(
            f"    - Metric '{r['metric_name']}': {r['row_count']} samples, avg={float(r['avg_val']):.4f}"
        )
    if expected_metric_names:
        metric_names = {r["metric_name"] for r in rows}
        missing = set(expected_metric_names) - metric_names
        if missing:
            raise AssertionError(
                f"Expected metrics {sorted(missing)} not found in ClickHouse for XID {xid}. "
                f"Found: {sorted(metric_names)}"
            )


def _verify_clickhouse_sql_metrics_once(
    endpoint: str,
    xid: str,
    expected_metric_names: Sequence[str] | set[str] | None = None,
    expected_min_rows: int = 1,
) -> dict[str, Any] | None:
    """Queries ClickHouse once and asserts row arrival and metric subset presence."""
    numeric_xid = int(xid) if xid.isdigit() else 0
    query = (
        f"SELECT metric_name, count() as row_count, avg(metric_value) as avg_val, "
        f"min(metric_value) as min_val, max(metric_value) as max_val "
        f"FROM xmc_metrics WHERE xid = {numeric_xid} GROUP BY metric_name FORMAT JSON"
    )
    res = query_clickhouse_sql(endpoint, query)
    rows = res.get("data") if isinstance(res, dict) else None
    if not rows:
        return None
    total_rows = sum(int(r.get("row_count", 0)) for r in rows)
    if total_rows < expected_min_rows:
        return None
    _log_and_check_clickhouse_metrics(xid, total_rows, rows, expected_metric_names)
    return {"total_rows": total_rows, "metrics": rows}


def validate_clickhouse_metrics(
    endpoint: str,
    xid: str,
    expected_metric_names: Sequence[str] | set[str] | None = None,
    expected_min_rows: int = 1,
    timeout_sec: int = 60,
    poll_interval_sec: int = 3,
    strict_required: bool = True,
) -> dict[str, Any]:
    """Polls and asserts metric row arrival and value validity directly via ClickHouse SQL."""
    start_time, last_error = time.monotonic(), None
    while time.monotonic() - start_time < timeout_sec:
        try:
            if res := _verify_clickhouse_sql_metrics_once(
                endpoint, xid, expected_metric_names, expected_min_rows
            ):
                return res
        except Exception as e:
            last_error = e
        time.sleep(poll_interval_sec)

    if strict_required:
        raise AssertionError(
            f"ClickHouse SQL metrics validation timed out after {timeout_sec}s for XID {xid}. "
            f"Last error: {last_error}"
        )
    print(f"  [i] ClickHouse check skipped/notice: {last_error}")
    return {"total_rows": 0, "metrics": [], "skipped": True}


def _build_dashboard_headers() -> dict[str, str]:
    """Builds HTTP request headers for Dashboard Service REST calls."""
    headers = {"Accept": "application/json", "User-Agent": "xmc-validator/1.0"}
    if token := (os.environ.get("XMC_AUTH_TOKEN") or os.environ.get("AUTH_TOKEN") or "").strip():
        headers["Authorization"] = f"Bearer {token}"
    return headers


def query_dashboard_http(url: str, timeout_sec: int = 10) -> dict[str, Any]:
    """Executes an HTTP GET request against the Dashboard Service REST gateway."""
    return _http_get(
        url,
        headers=_build_dashboard_headers(),
        timeout_sec=timeout_sec,
        service_name="Dashboard",
    )


def _count_hydrated_chart_points(plots_data: list[dict[str, Any]]) -> int:
    """Counts hydrated series points and summary KPI values across plotsData."""
    total_points = 0
    for plot in plots_data:
        scatter = plot.get("scatterSeries") or plot.get("scatter_series") or {}
        total_points += len(scatter.get("points") or [])
        summary = plot.get("summaryData") or plot.get("summary_data") or {}
        if summary.get("metricName") or summary.get("metric_name"):
            total_points += 1
    return total_points


def _verify_single_chart_data(
    base: str, dashboard_id: str, chart_id: str, min_hydrated_points: int
) -> int:
    """Queries GetChartData for a single chart and asserts hydrated point threshold."""
    chart_data = query_dashboard_http(f"{base}/charts/{chart_id}:getData")
    plots_data = chart_data.get("plotsData") or chart_data.get("plots_data") or []
    if not plots_data:
        raise AssertionError(f"GetChartData for '{chart_id}' returned empty plotsData.")
    pts = _count_hydrated_chart_points(plots_data)
    if pts < min_hydrated_points:
        raise AssertionError(
            f"GetChartData for '{chart_id}' returned {pts} points (< {min_hydrated_points})."
        )
    print(
        f"  [✓] Verified Dashboard '{dashboard_id}' & GetChartData ('{chart_id}'): {len(plots_data)} plots, {pts} points."
    )
    return pts


def _verify_dashboard_and_chart_data_once(
    base_endpoint: str,
    dashboard_id: str,
    expected_chart_id: str | None = None,
    min_hydrated_points: int = 1,
) -> dict[str, Any]:
    """Fetches dashboard metadata and GetChartData once and asserts hydrated points."""
    base = (
        f"{base_endpoint.rstrip('/').removesuffix('/v1/dashboards')}/v1/dashboards/{dashboard_id}"
    )
    dash_data = query_dashboard_http(base)
    chart_ids = dash_data.get("chartIds") or dash_data.get("chart_ids") or []
    if not chart_ids:
        raise AssertionError(f"Dashboard '{dashboard_id}' has no associated chartIds.")
    target_ids = [
        str(cid).split("/")[-1] for cid in ([expected_chart_id] if expected_chart_id else chart_ids)
    ]
    total_pts = sum(
        _verify_single_chart_data(base, dashboard_id, cid, min_hydrated_points)
        for cid in target_ids
    )
    return {"dashboard_id": dashboard_id, "chart_id": target_ids[0], "total_points": total_pts}


def _is_fatal_dashboard_error(err: Exception) -> bool:
    """Returns True if the error indicates a fatal unconfigured DataEngine."""
    return isinstance(err, AssertionError) and "data engine is not configured" in str(err).lower()


def validate_dashboard_chart_data(
    base_endpoint: str,
    dashboard_id: str,
    expected_chart_id: str | None = None,
    min_hydrated_points: int = 1,
    timeout_sec: int = 20,
    poll_interval_sec: int = 2,
    strict_required: bool = True,
) -> dict[str, Any]:
    """Verifies Dashboard metadata and GetChartData hydration via Dashboard Service."""
    start_time, last_error = time.monotonic(), None
    while time.monotonic() - start_time < timeout_sec:
        try:
            return _verify_dashboard_and_chart_data_once(
                base_endpoint, dashboard_id, expected_chart_id, min_hydrated_points
            )
        except Exception as err:
            if _is_fatal_dashboard_error(err):
                raise
            last_error = err
            if not isinstance(err, AssertionError) and not strict_required:
                break
        time.sleep(poll_interval_sec)
    if strict_required:
        raise AssertionError(f"Dashboard GetChartData failed ({dashboard_id}): {last_error}")
    print(f"  [i] Dashboard check skipped/notice: {last_error}")
    return {"skipped": True}
