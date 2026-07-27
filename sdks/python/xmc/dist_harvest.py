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
    env: Mapping[str, str] | None = None,
    log_all_replicas: bool = False,
) -> tuple[str, str, str, str, bool]:
    """Harvests mandatory XMC environment variables (XMC_XID, XMC_WORK_UNIT_ID, XMC_EXECUTION_ID, XMC_REPLICA_ID).

    Returns:
        tuple[xid, work_unit_id, execution_id, replica_id, is_silenced]

    Fail-Closed Safety:
        XMC_XID, XMC_WORK_UNIT_ID, and XMC_EXECUTION_ID are mandatory. If any are missing or empty,
        the SDK logs an error and sets is_silenced=True to disable metric ingestion for misconfigured pods.
    """
    if env is None:
        env = os.environ

    raw_xid = env.get("XMC_XID", "").strip()
    raw_wu = env.get("XMC_WORK_UNIT_ID", "").strip()
    raw_exec = env.get("XMC_EXECUTION_ID", "").strip()

    if not raw_xid or not raw_wu or not raw_exec:
        logger.error(
            "XMC metadata environment variables missing or empty (XMC_XID=%r, XMC_WORK_UNIT_ID=%r, XMC_EXECUTION_ID=%r). "
            "Disabling metrics ingestion for misconfigured pod.",
            raw_xid,
            raw_wu,
            raw_exec,
        )
        return "", "", "", "0", True

    raw_replica = env.get("XMC_REPLICA_ID", "").strip()
    if raw_replica:
        replica_id = raw_replica
        is_silenced = False
    else:
        replica_id, is_silenced = get_replica_id_and_silence_status(
            env=env, log_all_replicas=log_all_replicas
        )

    return raw_xid, raw_wu, raw_exec, replica_id, is_silenced


# Alias matching CamelCase specification for backward compatibility.
GetReplicaIDAndSilenceStatus = get_replica_id_and_silence_status
