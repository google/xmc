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
import time
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
    """Raised when a work unit enters a terminal failure state."""


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
    try:
        if (
            hasattr(work_unit, "current_work_unit_execution")
            and work_unit.current_work_unit_execution.HasField("status")
            and work_unit.current_work_unit_execution.status.state
        ):
            return int(work_unit.current_work_unit_execution.status.state)
    except Exception:
        pass

    try:
        req = _make_req("GetCurrentWorkUnitExecutionRequest", name=work_unit.name)
        cur_exec = stub.GetCurrentWorkUnitExecution(req)
        if cur_exec and hasattr(cur_exec, "status") and cur_exec.status.state:
            return int(cur_exec.status.state)
    except Exception:
        pass

    return STATE_UNSPECIFIED


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
    timeout_sec: int = 300,
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

        try:
            wu_results, all_completed = _evaluate_work_units(
                stub=stub,
                work_units=work_units,
                expected_count=expected_count,
                expected_queue=expected_queue,
                expected_worker_names=resolved_worker_names,
            )

            current_states = {r["worker_name"]: r["state"] for r in wu_results}
            if current_states != last_reported_states:
                print(f"[{int(elapsed)}s] WorkUnit States: {current_states}")
                last_reported_states = current_states

            if not wait_for_completion or all_completed:
                return wu_results

        except TerminalFailureError:
            raise
        except AssertionError:
            if elapsed >= timeout_sec:
                raise
            time.sleep(poll_interval_sec)
            continue

        if elapsed >= timeout_sec:
            raise TimeoutError(
                f"WorkUnits did not reach COMPLETED state within {timeout_sec}s timeout."
            )

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
