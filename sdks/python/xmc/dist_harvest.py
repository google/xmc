# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Distributed ML rank harvesting and replica detection heuristics for XMC."""

import logging
import os
from collections.abc import Mapping
from typing import Any

logger = logging.getLogger(__name__)

# Environment variable keys for rank detection stages
_STRICT_RANK_ENV_VARS = (
    "RANK",  # PyTorch Distributed / TorchRun
    "JAX_PROCESS_INDEX",  # JAX Multi-Host
    "OMPI_COMM_WORLD_RANK",  # OpenMPI / Slurm
)

_GENERIC_DIST_ENV_VARS = (
    "RAY_ADDRESS",  # Ray Cluster
    "MASTER_ADDR",  # PyTorch DDP Master Address
)

_ORCHESTRATOR_INDEX_ENV_VARS = (
    "XMC_JOB_INDEX",  # XMC Orchestrator Index
    "JOB_COMPLETION_INDEX",  # K8s Indexed Job Index
)


def _check_stage1_strict_rank(env: Mapping[str, str]) -> str | None:
    """Stage 1: Checks strict process-level framework rank variables."""
    for key in _STRICT_RANK_ENV_VARS:
        val = env.get(key)
        if val is not None:
            stripped = val.strip()
            if stripped:
                return stripped
    return None


def _check_stage2_generic_indicators(env: Mapping[str, str]) -> bool:
    """Stage 2: Checks generic distributed ML environment indicators."""
    world_size_str = env.get("WORLD_SIZE")
    if world_size_str is not None:
        stripped_world_size = world_size_str.strip()
        if stripped_world_size:
            try:
                if int(stripped_world_size) > 1:
                    return True
            except ValueError:
                pass

    for key in _GENERIC_DIST_ENV_VARS:
        val = env.get(key)
        if val is not None and val.strip():
            return True

    return False


def _check_stage3_orchestrator_index(env: Mapping[str, str]) -> str | None:
    """Stage 3: Checks orchestrator pod-level job completion index variables."""
    for key in _ORCHESTRATOR_INDEX_ENV_VARS:
        val = env.get(key)
        if val is not None:
            stripped = val.strip()
            if stripped:
                return stripped
    return None


def _is_rank_zero(rank: str) -> bool:
    """Returns True if rank represents rank zero (e.g. '0', '00')."""
    try:
        return int(rank) == 0
    except ValueError:
        return rank == "0"


def get_replica_id_and_silence_status(
    env: Mapping[str, str] | None = None,
    log_all_replicas: bool = False,
) -> tuple[str, bool]:
    """Harvests distributed ML rank/replica ID and calculates silencing status.

    Evaluates environment in 3 stages:
    1. Strict Framework Ranks: Checks RANK (PyTorch), JAX_PROCESS_INDEX (JAX),
       OMPI_COMM_WORLD_RANK (MPI).
    2. Fail-Closed Safety Net: Checks generic indicators (WORLD_SIZE > 1,
       RAY_ADDRESS, MASTER_ADDR). If present without strict rank, silences.
    3. Orchestrator Index: Checks XMC_JOB_INDEX or JOB_COMPLETION_INDEX.
       Defaults to "0" if missing.

    Args:
        env: Environment variables mapping. Defaults to os.environ.
        log_all_replicas: If True, bypasses rank-zero silencer and never silences.

    Returns:
        Tuple of (replica_id, should_silence).
    """
    if env is None:
        env = os.environ

    # Stage 1: Strict Framework Ranks
    rank = _check_stage1_strict_rank(env)
    if rank is not None:
        should_silence = False if log_all_replicas else not _is_rank_zero(rank)
        return rank, should_silence

    # Stage 2: Generic Distributed Indicators (Fail-Closed Safety Net)
    if _check_stage2_generic_indicators(env):
        logger.warning(
            "Generic distributed ML indicators detected without strict rank. "
            "Silencing metrics logging (fail-closed)."
        )
        should_silence = not log_all_replicas
        return "-1", should_silence

    # Stage 3: Single-Node / Orchestrator Fallback
    orch_index = _check_stage3_orchestrator_index(env)
    if orch_index is not None:
        should_silence = False if log_all_replicas else not _is_rank_zero(orch_index)
        return orch_index, should_silence

    return "0", False


def harvest_client_metadata(
    xid: int | str | None = None,
    work_unit_id: int | str | None = None,
    execution_id: int | str | None = None,
    replica_id: str | None = None,
    env: Mapping[str, str] | None = None,
    log_all_replicas: bool = False,
    fallback_client: Any | None = None,
) -> tuple[str, str, str, str]:
    """Harvests client-level metadata constants (xid, work_unit_id, execution_id, replica_id).

    Checks in order: constructor parameter -> fallback_client attribute -> env variable -> default ("0").

    WHY string return types & "0" defaults: Though xid, work_unit_id, and execution_id are
    64-bit integers in Spanner/ClickHouse, transmitting 64-bit numbers as raw JSON integers
    causes precision truncation in JavaScript IEEE 754 dashboards (capped at 2^53 - 1).
    Stringifying integer IDs preserves precision per Google AIP-127 and XMCMetricsBatch schema.
    Returning "0" string defaults ensures schema-compliant payloads in un-orchestrated setups.
    """
    if env is None:
        env = os.environ

    def _get_str(val: Any, attr_name: str, env_key: str) -> str:
        if val is not None and not isinstance(val, (dict, list)):
            return str(val)
        if fallback_client is not None:
            fb = getattr(fallback_client, f"_{attr_name}", None)
            if fb is None:
                fb = getattr(fallback_client, attr_name, None)
            if fb is not None and isinstance(fb, (int, str)):
                return str(fb)
        env_val = env.get(env_key)
        if env_val is not None:
            return str(env_val)
        return "0"

    res_xid = _get_str(xid, "xid", "XMC_XID")
    res_work_unit_id = _get_str(work_unit_id, "work_unit_id", "XMC_WORK_UNIT_ID")
    res_execution_id = _get_str(execution_id, "execution_id", "XMC_EXECUTION_ID")

    str_repid: str | None = None
    if replica_id is not None:
        str_repid = str(replica_id)
    elif fallback_client is not None:
        fb = getattr(fallback_client, "_replica_id", None)
        if fb is None:
            fb = getattr(fallback_client, "replica_id", None)
        if fb is not None and isinstance(fb, str):
            str_repid = fb
    if str_repid is None:
        str_repid = env.get("XMC_REPLICA_ID") or None

    if str_repid is not None:
        res_replica_id = str_repid
    else:
        res_replica_id, _ = get_replica_id_and_silence_status(
            env=env, log_all_replicas=log_all_replicas
        )

    return res_xid, res_work_unit_id, res_execution_id, res_replica_id


# Alias matching CamelCase specification if referenced.
GetReplicaIDAndSilenceStatus = get_replica_id_and_silence_status
