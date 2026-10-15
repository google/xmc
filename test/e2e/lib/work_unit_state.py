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

"""Work unit lifecycle states observed through the XMC REST API in E2E tests."""

from __future__ import annotations

import logging
from collections.abc import Callable, Mapping
from typing import Any

from test.e2e.lib.polling import TransientProbeError, poll_until

TERMINAL_STATES = frozenset({"COMPLETED", "FAILED", "STOPPED"})
UNSPECIFIED_STATE = "WORK_UNIT_STATE_UNSPECIFIED"
DEFAULT_STATE_TIMEOUT_SECONDS = 180.0
DEFAULT_STATE_POLL_INTERVAL_SECONDS = 2.0
# HTTP statuses of temporary failures, such as an overloaded or restarting API server behind
# the gateway, which work unit polling retries until its deadline.
TRANSIENT_HTTP_STATUSES = frozenset({429, 502, 503, 504})

logger = logging.getLogger(__name__)

WorkUnit = Mapping[str, Any]


def work_unit_name_from_operation(operation_name: str) -> str:
    """Returns the name of the work unit that a CreateWorkUnit operation creates.

    Args:
        operation_name: Operation name such as experiments/1/workUnits/2/operations/<id>.

    Returns:
        The work unit resource name, such as experiments/1/workUnits/2.

    Raises:
        ValueError: If operation_name is not the name of a work unit operation.
    """
    work_unit_name, separator, operation_id = operation_name.rpartition("/operations/")
    if not separator or "/workUnits/" not in work_unit_name or not operation_id:
        raise ValueError(f"Invalid work unit operation name: {operation_name!r}")
    return work_unit_name


def work_unit_fetch_error(work_unit_name: str, status: int, body: str) -> RuntimeError:
    """Returns the error to raise for a failed GET request for a work unit.

    Args:
        work_unit_name: Name of the requested work unit.
        status: HTTP status of the failed response.
        body: Text of the failed response, included in the error message.

    Returns:
        A TransientProbeError, which polling retries, if status reports a temporary failure;
        otherwise a RuntimeError.
    """
    message = f"Failed to get {work_unit_name}: {status} {body}"
    if status in TRANSIENT_HTTP_STATUSES:
        return TransientProbeError(message)
    return RuntimeError(message)


def work_unit_state(work_unit: WorkUnit) -> str:
    """Returns the state of the current execution of a work unit's REST representation."""
    return _execution_status(work_unit).get("state") or UNSPECIFIED_STATE


def _execution_status(work_unit: WorkUnit) -> Mapping[str, Any]:
    """Returns the status of the current execution of a work unit's REST representation."""
    return (work_unit.get("currentWorkUnitExecution") or {}).get("status") or {}


def wait_for_work_unit_state(
    fetch_work_unit: Callable[[], WorkUnit],
    expected_state: str,
    timeout_seconds: float = DEFAULT_STATE_TIMEOUT_SECONDS,
    poll_interval_seconds: float = DEFAULT_STATE_POLL_INTERVAL_SECONDS,
) -> WorkUnit:
    """Polls a work unit until its current execution reaches expected_state.

    Args:
        fetch_work_unit: Returns the REST representation of the work unit. It may raise
            TransientProbeError on a temporary failure, which is retried until the deadline;
            any other exception propagates.
        expected_state: Name of the awaited WorkUnitState, such as COMPLETED.
        timeout_seconds: Seconds after which waiting gives up.
        poll_interval_seconds: Seconds between fetches.

    Returns:
        The REST representation in which the expected state was observed.

    Raises:
        RuntimeError: If the work unit reaches a different terminal state.
        PollTimeoutError: If the state is not reached before the deadline.
    """
    return poll_until(
        _state_probe(fetch_work_unit, expected_state),
        f"work unit state {expected_state}",
        timeout_seconds,
        poll_interval_seconds,
    )


def _state_probe(
    fetch_work_unit: Callable[[], WorkUnit], expected_state: str
) -> Callable[[], WorkUnit | None]:
    """Returns a probe that logs state changes and fails fast on unexpected terminal states."""
    observed: list[str] = []

    def probe() -> WorkUnit | None:
        work_unit = fetch_work_unit()
        state = work_unit_state(work_unit)
        if not observed or observed[-1] != state:
            observed.append(state)
            logger.info("Work unit %s is %s.", work_unit.get("name"), state)
        if state == expected_state:
            return work_unit
        if state in TERMINAL_STATES:
            message = (_execution_status(work_unit).get("systemMessage") or {}).get(
                "userFacingText", ""
            )
            raise RuntimeError(
                f"Work unit {work_unit.get('name')} reached {state} instead of "
                f"{expected_state}: {message!r}"
            )
        return None

    return probe
