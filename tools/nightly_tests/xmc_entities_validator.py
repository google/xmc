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


def extract_work_unit_state(
    stub: Any,
    work_unit: Any,
) -> int:
    """Retrieves current WorkUnitState enum integer for a WorkUnit."""
    cached_state = STATE_UNSPECIFIED
    has_cached = False
    if (
        hasattr(work_unit, "current_work_unit_execution")
        and work_unit.current_work_unit_execution is not None
        and hasattr(work_unit.current_work_unit_execution, "HasField")
        and work_unit.current_work_unit_execution.HasField("status")
        and getattr(work_unit.current_work_unit_execution.status, "state", None) is not None
    ):
        try:
            cached_state = int(work_unit.current_work_unit_execution.status.state)
            has_cached = True
        except (TypeError, ValueError):
            pass

    if has_cached and cached_state in (
        STATE_UNSPECIFIED,
        STATE_COMPLETED,
        STATE_FAILED,
        STATE_PARTIALLY_FAILED,
    ):
        return cached_state

    wu_name = getattr(work_unit, "name", "unknown")
    if stub is not None and wu_name != "unknown":
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
            logging.warning(
                "Failed to retrieve current WorkUnitExecution for %s: %s",
                wu_name,
                e,
                exc_info=True,
            )

    return cached_state


def validate_experiment(
    stub: Any,
    xid: str,
    expected_title_pattern: str | None = None,
) -> Any:
    """Fetches and verifies experiment existence and title.

    Args:
        stub: gRPC stub for ExperimentStateService.
        xid: Experiment XID.
        expected_title_pattern: Optional substring expected in the experiment title.

    Returns:
        The fetched Experiment protobuf object.

    Raises:
        AssertionError: If experiment is not found or title does not match.
    """
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

    found_worker_names: list[str] = []
    wu_results: list[dict[str, Any]] = []
    all_completed = True
    has_failure = False

    for wu in work_units:
        wu_id = getattr(wu, "name", "").split("/")[-1]
        worker_name = extract_worker_name(wu)
        found_worker_names.append(worker_name)
        queue_name = extract_queue_name(wu)

        if expected_queue is not None and queue_name != expected_queue:
            raise AssertionError(
                f"WorkUnit {getattr(wu, 'name', '')} targeted queue '{queue_name}', "
                f"expected '{expected_queue}'."
            )

        state_int = extract_work_unit_state(stub, wu)
        state_str = state_enum_to_name(state_int)

        if state_int in (STATE_FAILED, STATE_PARTIALLY_FAILED):
            has_failure = True
        if state_int != STATE_COMPLETED:
            all_completed = False

        wu_results.append(
            {
                "id": wu_id,
                "name": getattr(wu, "name", ""),
                "worker_name": worker_name,
                "queue": queue_name,
                "state_enum": state_int,
                "state": state_str,
            }
        )

    if expected_worker_names is not None:
        missing_workers = set(expected_worker_names) - set(found_worker_names)
        if missing_workers:
            raise AssertionError(
                f"Expected worker names {list(expected_worker_names)} not satisfied. "
                f"Missing: {sorted(missing_workers)}, Found: {sorted(found_worker_names)}."
            )

    if has_failure:
        failed_units = [r for r in wu_results if "FAILED" in r["state"]]
        raise TerminalFailureError(
            f"One or more WorkUnits failed execution: {json.dumps(failed_units, indent=2)}"
        )

    return wu_results, all_completed


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
    """Fetches, validates, and polls work units until all assertions pass.

    Args:
        stub: gRPC stub for ExperimentStateService.
        xid: Experiment XID.
        expected_count: Optional expected number of work units.
        expected_queue: Optional expected target ClusterQueue name.
        expected_worker_names: Optional sequence of expected worker names. If omitted,
            worker names are not asserted.
        timeout_sec: Maximum timeout to poll for completion.
        poll_interval_sec: Polling interval in seconds.
        wait_for_completion: Whether to wait for terminal COMPLETED state.

    Returns:
        List of verified work unit metadata dictionaries.

    Raises:
        AssertionError: If assertions on count, names, queues, or terminal states fail.
        TimeoutError: If execution does not complete within timeout.
    """
    parent_name = f"experiments/{xid}"
    resolved_worker_names: list[str] | None = (
        list(expected_worker_names) if expected_worker_names is not None else None
    )

    start_time = time.monotonic()
    last_reported_states: dict[str, str] = {}
    last_reported_time: float = -1.0

    while True:
        elapsed = time.monotonic() - start_time
        try:
            req = _make_req("ListWorkUnitsRequest", parent=parent_name)
            response = stub.ListWorkUnits(req)
            work_units = list(getattr(response, "work_units", []))
        except Exception as e:
            if elapsed >= timeout_sec:
                raise
            logging.warning("Transient error fetching work units: %s. Retrying...", e)
            time.sleep(poll_interval_sec)
            continue

        min_expected = (
            expected_count
            if expected_count is not None
            else (len(resolved_worker_names) if resolved_worker_names else 0)
        )
        if len(work_units) < min_expected:
            if elapsed >= timeout_sec:
                raise AssertionError(
                    f"Expected at least {min_expected} WorkUnits for {parent_name}, "
                    f"but only found {len(work_units)} after {int(elapsed)}s."
                )
            time.sleep(poll_interval_sec)
            continue

        if expected_count is not None and len(work_units) > expected_count:
            raise AssertionError(
                f"WorkUnit count exceeded: expected {expected_count}, found {len(work_units)}."
            )

        wu_results, all_completed = _evaluate_work_units(
            stub=stub,
            work_units=work_units,
            expected_count=expected_count,
            expected_queue=expected_queue,
            expected_worker_names=resolved_worker_names,
        )

        current_states = {r["worker_name"]: r["state"] for r in wu_results}
        if current_states != last_reported_states or (elapsed - last_reported_time >= 30.0):
            print(f"[{int(elapsed)}s] WorkUnit States: {current_states}", flush=True)
            last_reported_states = current_states
            last_reported_time = elapsed

        if not wait_for_completion or all_completed:
            return wu_results

        if elapsed >= timeout_sec:
            raise TimeoutError(
                f"WorkUnits did not reach COMPLETED state within {timeout_sec}s timeout."
            )

        time.sleep(poll_interval_sec)


def validate_jobsets(
    work_unit: Any,
    expected_jobs: Sequence[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Validates constituent JobSet jobs, replica counts, init containers, and sidecars.

    Args:
        work_unit: WorkUnit protobuf message or duck-typed object.
        expected_jobs: Sequence of expected job specification dictionaries. Each dict can contain:
            - "name" (str): Expected job name (required).
            - "replicas" (int, optional): Expected replica count.
            - "init_containers" (Sequence[str | dict[str, Any]] | int, optional):
                Expected init container names, spec dictionaries, or count.
            - "sidecars" (Sequence[str | dict[str, Any]] | int, optional):
                Expected sidecar container names, spec dictionaries, or count.
            - "spec" or "primary_container" (str | dict[str, Any], optional):
                Expected primary container name or spec dictionary.

    Returns:
        List of validated job metadata dictionaries.

    Raises:
        AssertionError: If JobSet structure or expected job specs do not match.
    """
    if not hasattr(work_unit, "executable"):
        raise AssertionError(
            f"WorkUnit '{getattr(work_unit, 'name', 'unknown')}' missing executable field."
        )

    executable = work_unit.executable
    if hasattr(executable, "HasField") and not executable.HasField("jobset"):
        raise AssertionError(
            f"WorkUnit '{getattr(work_unit, 'name', 'unknown')}' does not contain a JobSet executable."
        )

    jobset = getattr(executable, "jobset", None)
    if jobset is None or not hasattr(jobset, "jobs"):
        raise AssertionError(
            f"WorkUnit '{getattr(work_unit, 'name', 'unknown')}' has invalid or empty JobSet."
        )

    actual_jobs = list(jobset.jobs)
    if len(actual_jobs) != len(expected_jobs):
        raise AssertionError(
            f"JobSet job count mismatch: expected {len(expected_jobs)}, found {len(actual_jobs)}."
        )

    actual_jobs_by_name = {getattr(j, "name", ""): j for j in actual_jobs}
    validated_results: list[dict[str, Any]] = []

    for idx, expected_job in enumerate(expected_jobs):
        expected_name = expected_job.get("name")
        if not expected_name:
            raise AssertionError(f"Expected job at index {idx} must specify a 'name'.")

        actual_job = actual_jobs_by_name.get(expected_name)
        if actual_job is None:
            # Fallback to positional matching if names don't directly match
            if idx < len(actual_jobs):
                actual_job = actual_jobs[idx]
            else:
                raise AssertionError(
                    f"Expected job '{expected_name}' not found in JobSet jobs: "
                    f"{[getattr(j, 'name', '') for j in actual_jobs]}."
                )

        # Validate replicas
        if "replicas" in expected_job:
            expected_replicas = int(expected_job["replicas"])
            actual_replicas = getattr(actual_job, "replicas", 1)
            if actual_replicas != expected_replicas:
                raise AssertionError(
                    f"Job '{expected_name}' replica count mismatch: "
                    f"expected {expected_replicas}, found {actual_replicas}."
                )

        # Validate init containers
        actual_inits = list(getattr(actual_job, "init_containers", []))
        if "init_containers" in expected_job:
            expected_inits = expected_job["init_containers"]
            if isinstance(expected_inits, int):
                if len(actual_inits) != expected_inits:
                    raise AssertionError(
                        f"Job '{expected_name}' init container count mismatch: "
                        f"expected {expected_inits}, found {len(actual_inits)}."
                    )
            elif isinstance(expected_inits, (list, tuple)):
                if len(actual_inits) != len(expected_inits):
                    raise AssertionError(
                        f"Job '{expected_name}' init container count mismatch: "
                        f"expected {len(expected_inits)}, found {len(actual_inits)}."
                    )
                for exp_c, act_c in zip(expected_inits, actual_inits, strict=False):
                    if isinstance(exp_c, str):
                        act_name = getattr(act_c, "name", "")
                        if act_name != exp_c:
                            raise AssertionError(
                                f"Job '{expected_name}' init container name mismatch: "
                                f"expected '{exp_c}', found '{act_name}'."
                            )
                    elif isinstance(exp_c, dict):
                        for k, v in exp_c.items():
                            act_val = getattr(act_c, k, None)
                            if act_val != v:
                                raise AssertionError(
                                    f"Job '{expected_name}' init container property '{k}' mismatch: "
                                    f"expected '{v}', found '{act_val}'."
                                )

        # Validate sidecars
        actual_sidecars = list(getattr(actual_job, "sidecars", []))
        if "sidecars" in expected_job:
            expected_sidecars = expected_job["sidecars"]
            if isinstance(expected_sidecars, int):
                if len(actual_sidecars) != expected_sidecars:
                    raise AssertionError(
                        f"Job '{expected_name}' sidecar container count mismatch: "
                        f"expected {expected_sidecars}, found {len(actual_sidecars)}."
                    )
            elif isinstance(expected_sidecars, (list, tuple)):
                if len(actual_sidecars) != len(expected_sidecars):
                    raise AssertionError(
                        f"Job '{expected_name}' sidecar container count mismatch: "
                        f"expected {len(expected_sidecars)}, found {len(actual_sidecars)}."
                    )
                for exp_s, act_s in zip(expected_sidecars, actual_sidecars, strict=False):
                    if isinstance(exp_s, str):
                        act_name = getattr(act_s, "name", "")
                        if act_name != exp_s:
                            raise AssertionError(
                                f"Job '{expected_name}' sidecar container name mismatch: "
                                f"expected '{exp_s}', found '{act_name}'."
                            )
                    elif isinstance(exp_s, dict):
                        for k, v in exp_s.items():
                            act_val = getattr(act_s, k, None)
                            if act_val != v:
                                raise AssertionError(
                                    f"Job '{expected_name}' sidecar container property '{k}' mismatch: "
                                    f"expected '{v}', found '{act_val}'."
                                )

        # Validate primary container spec
        expected_spec = expected_job.get("spec") or expected_job.get("primary_container")
        actual_spec = getattr(actual_job, "spec", None)
        if expected_spec is not None:
            if actual_spec is None:
                raise AssertionError(f"Job '{expected_name}' missing primary container spec.")
            if isinstance(expected_spec, str):
                act_name = getattr(actual_spec, "name", "")
                if act_name != expected_spec:
                    raise AssertionError(
                        f"Job '{expected_name}' primary container name mismatch: "
                        f"expected '{expected_spec}', found '{act_name}'."
                    )
            elif isinstance(expected_spec, dict):
                for k, v in expected_spec.items():
                    act_val = getattr(actual_spec, k, None)
                    if act_val != v:
                        raise AssertionError(
                            f"Job '{expected_name}' primary container property '{k}' mismatch: "
                            f"expected '{v}', found '{act_val}'."
                        )

        validated_results.append(
            {
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
        )

    return validated_results


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


def validate_artifacts(
    stub: Any,
    xid: str | int,
    wid: str | int | None = None,
    expected_artifact_types: Sequence[str] | None = None,
    expected_metric_names: Sequence[str] | None = None,
    timeout_sec: int = 60,
    poll_interval_sec: int = 2,
) -> list[dict[str, Any]]:
    """Fetches and validates artifacts associated with an Experiment or WorkUnit.

    Asserts the presence and health of ChartArtifact, UrlArtifact, and custom artifacts via ESS API.

    Args:
        stub: gRPC stub for ExperimentStateService.
        xid: Experiment XID or full resource path.
        wid: Optional WorkUnit ID. If provided, queries WorkUnit-level artifacts.
        expected_artifact_types: Optional sequence of artifact types expected (e.g. ['chart', 'url']).
        expected_metric_names: Optional sequence of metric names expected in ChartArtifact payloads.
        timeout_sec: Maximum timeout to poll for artifacts.
        poll_interval_sec: Polling interval in seconds.

    Returns:
        List of verified artifact metadata dictionaries.

    Raises:
        AssertionError: If expected artifacts or metrics are missing.
        TerminalFailureError: If a ChartArtifact enters FAILED provisioning status.
        TimeoutError: If artifact conditions are not met within timeout.
    """
    str_xid = str(xid).strip()
    if wid is not None:
        if str_xid.startswith("experiments/"):
            parent_name = f"{str_xid}/workUnits/{wid}"
        else:
            parent_name = f"experiments/{str_xid}/workUnits/{wid}"
    elif str_xid.startswith("experiments/"):
        parent_name = str_xid
    else:
        parent_name = f"experiments/{str_xid}"

    normalized_expected_types = (
        {_normalize_artifact_type(t) for t in expected_artifact_types}
        if expected_artifact_types is not None
        else None
    )
    expected_metrics_set = set(expected_metric_names) if expected_metric_names is not None else None

    start_time = time.monotonic()
    while True:
        elapsed = time.monotonic() - start_time
        try:
            req = _make_req("ListArtifactsRequest", parent=parent_name)
            response = stub.ListArtifacts(req)
            raw_artifacts = list(getattr(response, "artifacts", []))
        except Exception as e:
            if elapsed >= timeout_sec:
                raise
            logging.warning("Transient error fetching artifacts for %s: %s", parent_name, e)
            time.sleep(poll_interval_sec)
            continue

        artifact_results: list[dict[str, Any]] = []
        found_types: set[str] = set()
        found_metric_names: set[str] = set()

        for art in raw_artifacts:
            art_name = getattr(art, "name", "")
            art_parent = getattr(art, "parent", "")
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

            normalized_type = _normalize_artifact_type(payload_type) if payload_type else ""
            if normalized_type:
                found_types.add(normalized_type)

            # Chart artifact details
            chart_obj = (
                getattr(payload, "chart", None)
                if payload is not None
                else getattr(art, "chart", None)
            )
            metric_names = list(getattr(chart_obj, "metric_names", [])) if chart_obj else []
            chart_title = getattr(chart_obj, "title", "") if chart_obj else ""
            chart_status = getattr(chart_obj, "status", None) if chart_obj else None
            chart_err = getattr(chart_obj, "error_message", "") if chart_obj else ""

            if chart_status in (3, "FAILED", "STATUS_FAILED"):
                raise TerminalFailureError(
                    f"ChartArtifact '{art_name}' failed provisioning: {chart_err}"
                )

            found_metric_names.update(metric_names)

            # URL artifact details
            url_obj = (
                getattr(payload, "url", None) if payload is not None else getattr(art, "url", None)
            )
            url_str = getattr(url_obj, "url", "") if url_obj else getattr(art, "url", "")
            display_name = getattr(url_obj, "display_name", "") if url_obj else ""

            artifact_results.append(
                {
                    "name": art_name,
                    "parent": art_parent,
                    "type": payload_type or "unknown",
                    "normalized_type": normalized_type or "unknown",
                    "metric_names": metric_names,
                    "title": chart_title or display_name or getattr(art, "notes", ""),
                    "url": url_str if isinstance(url_str, str) else getattr(url_str, "url", ""),
                }
            )

        types_satisfied = (
            normalized_expected_types.issubset(found_types)
            if normalized_expected_types is not None
            else True
        )
        metrics_satisfied = (
            expected_metrics_set.issubset(found_metric_names)
            if expected_metrics_set is not None
            else True
        )

        if (
            types_satisfied
            and metrics_satisfied
            and (raw_artifacts or not (normalized_expected_types or expected_metrics_set))
        ):
            return artifact_results

        if elapsed >= timeout_sec:
            if not types_satisfied and normalized_expected_types:
                missing_types = sorted(normalized_expected_types - found_types)
                raise AssertionError(
                    f"Expected artifact types {missing_types} not found in {parent_name}. "
                    f"Found types: {sorted(found_types)}."
                )
            if not metrics_satisfied and expected_metrics_set:
                missing_metrics = sorted(expected_metrics_set - found_metric_names)
                raise AssertionError(
                    f"Expected metric names {missing_metrics} not found in ChartArtifacts for {parent_name}. "
                    f"Found metrics: {sorted(found_metric_names)}."
                )
            raise TimeoutError(f"Artifact verification timed out after {timeout_sec}s.")

        time.sleep(poll_interval_sec)


def validate_dashboard(
    dashboard_endpoint: str,
    expected_dashboards: Sequence[dict[str, Any] | str],
    timeout_sec: int = 60,
    poll_interval_sec: int = 2,
) -> list[dict[str, Any]]:
    """Validates Dashboards and Charts via Dashboard Service REST API endpoints.

    Asserts GET /v1/dashboards/{id} and GET /v1/charts/{id} endpoints.

    Args:
        dashboard_endpoint: Host:port or URL of Dashboard REST API (e.g. 'localhost:8080').
        expected_dashboards: Sequence of dashboard IDs or specification dicts. Each dict can contain:
            - "dashboard_id" (str): Dashboard slug/ID (required).
            - "expected_title" (str, optional): Expected substring in dashboard title.
            - "expected_chart_ids" (Sequence[str], optional): Expected referenced chart IDs.
        timeout_sec: Maximum timeout to poll for dashboard availability.
        poll_interval_sec: Polling interval in seconds.

    Returns:
        List of validated dashboard metadata dictionaries.

    Raises:
        AssertionError: If dashboard or chart assertions fail.
        TimeoutError: If endpoints fail to respond within timeout.
    """
    base_endpoint = dashboard_endpoint.strip().rstrip("/")
    if not base_endpoint.startswith(("http://", "https://")):
        base_endpoint = f"http://{base_endpoint}"

    normalized_expected: list[dict[str, Any]] = []
    for item in expected_dashboards:
        if isinstance(item, str):
            normalized_expected.append({"dashboard_id": item})
        else:
            normalized_expected.append(dict(item))

    start_time = time.monotonic()
    while True:
        elapsed = time.monotonic() - start_time
        dash_results: list[dict[str, Any]] = []
        try:
            for exp_dash in normalized_expected:
                raw_id = (
                    exp_dash.get("dashboard_id") or exp_dash.get("id") or exp_dash.get("name", "")
                )
                dash_id = raw_id.split("/", 1)[1] if raw_id.startswith("dashboards/") else raw_id
                if not dash_id:
                    raise AssertionError("Expected dashboard specification missing dashboard_id.")

                dash_url = f"{base_endpoint}/v1/dashboards/{dash_id}"
                req = urllib.request.Request(
                    dash_url,
                    headers={"Accept": "application/json", "User-Agent": "xmc-validator/1.0"},
                )
                with urllib.request.urlopen(req, timeout=10) as resp:
                    dash_data = json.loads(resp.read().decode("utf-8"))

                # Validate dashboard title
                dash_title = dash_data.get("title", "")
                if "expected_title" in exp_dash:
                    exp_title = exp_dash["expected_title"]
                    if exp_title not in dash_title:
                        raise AssertionError(
                            f"Dashboard '{dash_id}' title mismatch: "
                            f"expected '{exp_title}', found '{dash_title}'."
                        )

                # Validate chart IDs
                chart_ids = dash_data.get("chart_ids") or dash_data.get("chartIds") or []
                if "expected_chart_ids" in exp_dash:
                    exp_charts = exp_dash["expected_chart_ids"]
                    missing_charts = set(exp_charts) - set(chart_ids)
                    if missing_charts:
                        raise AssertionError(
                            f"Dashboard '{dash_id}' missing expected chart IDs: {sorted(missing_charts)}. "
                            f"Found: {sorted(chart_ids)}."
                        )

                # Validate each chart via GET /v1/charts/{id}
                verified_charts: list[dict[str, Any]] = []
                for cid in chart_ids:
                    chart_slug = cid.split("/", 1)[1] if cid.startswith("charts/") else cid
                    chart_url = f"{base_endpoint}/v1/charts/{chart_slug}"
                    c_req = urllib.request.Request(
                        chart_url,
                        headers={"Accept": "application/json", "User-Agent": "xmc-validator/1.0"},
                    )
                    with urllib.request.urlopen(c_req, timeout=10) as c_resp:
                        chart_data = json.loads(c_resp.read().decode("utf-8"))
                        verified_charts.append(chart_data)

                dash_results.append(
                    {
                        "dashboard_id": dash_id,
                        "name": dash_data.get("name", f"dashboards/{dash_id}"),
                        "title": dash_title,
                        "chart_ids": chart_ids,
                        "charts": verified_charts,
                    }
                )

            return dash_results

        except (urllib.error.URLError, ConnectionError, OSError) as e:
            if elapsed >= timeout_sec:
                raise TimeoutError(
                    f"Dashboard REST endpoint verification failed after {timeout_sec}s: {e}"
                )
            logging.warning(
                "Transient network error reaching dashboard endpoint: %s. Retrying...", e
            )
            time.sleep(poll_interval_sec)
        except AssertionError:
            if elapsed >= timeout_sec:
                raise
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
