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

Provides reusable assertions, gRPC client establishment, and basic validation
utilities for XMC experiment and workload execution.
"""

from __future__ import annotations

import logging
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any

try:
    import dotenv

    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
except ImportError:
    dotenv = None

try:
    from xmanager_cloud.experiment_state_server import experiment_state_api as ess_api
    from xmanager_cloud.experiment_state_server.proto import api_pb2 as ess_api_pb2
except ImportError:
    # Graceful fallback for environments/Bazel sandboxes without xmanager_cloud wheel
    ess_api = None
    ess_api_pb2 = None


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


def _call_stub(stub: Any, method_name: str, req: Any) -> Any:
    """Invokes a gRPC method on the stub, supporting both PascalCase and snake_case."""
    snake_name = "".join(["_" + c.lower() if c.isupper() else c for c in method_name]).lstrip("_")

    # If stub is a mock with specific mock children configured (e.g. in unit tests):
    if hasattr(stub, "_mock_children"):
        if snake_name in stub._mock_children:
            return getattr(stub, snake_name)(req)
        if method_name in stub._mock_children:
            return getattr(stub, method_name)(req)

    # For real gRPC stubs or standard objects, prefer PascalCase then snake_case:
    if hasattr(stub, method_name):
        return getattr(stub, method_name)(req)
    if hasattr(stub, snake_name):
        return getattr(stub, snake_name)(req)

    raise AttributeError(
        f"Stub {type(stub).__name__} has neither method '{method_name}' nor '{snake_name}'"
    )


def validate_experiment(
    stub: Any,
    xid: str,
) -> Any:
    """Fetches and verifies experiment existence.

    Args:
        stub: gRPC stub for ExperimentStateService.
        xid: Experiment XID.

    Returns:
        The fetched Experiment protobuf object.

    Raises:
        AssertionError: If experiment is not found.
    """
    exp_name = f"experiments/{xid}"
    req = _make_req("GetExperimentRequest", name=exp_name)
    experiment = _call_stub(stub, "GetExperiment", req)
    if not experiment or not getattr(experiment, "name", ""):
        raise AssertionError(f"Experiment {exp_name} could not be retrieved from ESS.")

    return experiment


def validate_work_units(
    stub: Any,
    xid: str,
    expected_count: int | None = None,
    expected_worker_names: Sequence[str] | None = None,
) -> list[dict[str, Any]]:
    """Fetches and validates work units against expected count and worker names.

    Args:
        stub: gRPC stub for ExperimentStateService.
        xid: Experiment XID.
        expected_count: Optional expected number of work units.
        expected_worker_names: Optional sequence of expected worker names.

    Returns:
        List of verified work unit metadata dictionaries.

    Raises:
        AssertionError: If assertions on count or worker names fail.
    """
    parent_name = f"experiments/{xid}"
    req = _make_req("ListWorkUnitsRequest", parent=parent_name)
    response = _call_stub(stub, "ListWorkUnits", req)
    work_units = list(getattr(response, "work_units", []))

    if expected_count is not None and len(work_units) != expected_count:
        raise AssertionError(
            f"WorkUnit count mismatch: expected {expected_count}, found {len(work_units)}."
        )

    found_worker_names: list[str] = []
    wu_results: list[dict[str, Any]] = []

    for wu in work_units:
        wu_id = getattr(wu, "name", "").split("/")[-1]
        worker_name = extract_worker_name(wu)
        found_worker_names.append(worker_name)
        wu_results.append(
            {
                "id": wu_id,
                "name": getattr(wu, "name", ""),
                "worker_name": worker_name,
            }
        )

    if expected_worker_names is not None:
        missing_workers = set(expected_worker_names) - set(found_worker_names)
        if missing_workers:
            raise AssertionError(
                f"Expected worker names {list(expected_worker_names)} not satisfied. "
                f"Missing: {sorted(missing_workers)}, Found: {sorted(found_worker_names)}."
            )

    return wu_results
