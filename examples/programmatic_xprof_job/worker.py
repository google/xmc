#!/usr/bin/env python3
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

"""A raw-JAX workload that captures a profile programmatically.

This worker relies solely on the environment variables injected by the XMC
Actuator. It reads the per-work-unit GCS destination from
``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` and uses native ``jax.profiler`` tracing
to write the profile there directly. The Diagon backend then discovers and loads
the profile from that GCS folder using the customized ManagedXprof MLRun labels.

After the trace is captured the workload idles for a fixed 30 minutes so the
enclosing JobSet stays alive long enough for the Diagon reconciler to create the
MLRun from the attached ManagedXprof artifact.
"""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_PROFILE_DURATION_SECONDS = flags.DEFINE_integer(
    "profile_duration_seconds",
    15,
    "How long to run the traced JAX computation while the profile is captured.",
)
_MATRIX_SIZE = flags.DEFINE_integer(
    "matrix_size",
    5000,
    "Side length of the square matrices multiplied during profiling.",
)

# Environment variable injected by the XMC Actuator into every container. It
# holds the full gs:// destination folder for this work unit's profile, e.g.
# gs://<bucket>/xmc-<xid>-<wid>-<timestamp>.
_XPROF_GCS_FOLDER_ENV = "XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER"


def _resolve_profile_dir() -> str:
    """Resolves the profile destination from the Actuator-injected env var.

    Returns:
      The GCS folder (``gs://...``) that the XMC Actuator injects via
      ``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` for this work unit.

    Raises:
      RuntimeError: if ``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` is not set, i.e.
        the workload is not running in a managed-xprof-configured XMC cluster.
    """
    profile_dir = os.environ.get(_XPROF_GCS_FOLDER_ENV)
    if not profile_dir:
        raise RuntimeError(
            f"{_XPROF_GCS_FOLDER_ENV} is not set. This workload must run in a"
            " managed-xprof-configured XMC cluster, where the Actuator injects"
            " the per-work-unit GCS destination folder."
        )
    return profile_dir


def _run_traced_workload(profile_dir: str, duration_seconds: int, matrix_size: int) -> None:
    """Runs matrix multiplications while a JAX profile trace is active.

    Args:
      profile_dir: Destination directory (local path or ``gs://`` URL) that the
        JAX profiler writes the captured trace to.
      duration_seconds: Minimum wall-clock time to keep the traced computation
        running so the profile captures representative steady-state work.
      matrix_size: Side length of the square matrices multiplied each step.
    """
    key = jax.random.PRNGKey(0)

    # Warm up first so the trace captures steady-state execution rather than
    # one-time JIT compilation.
    warmup = jnp.dot(
        jax.random.normal(key, (matrix_size, matrix_size)),
        jax.random.normal(key, (matrix_size, matrix_size)),
    )
    warmup.block_until_ready()

    print(f"Starting programmatic JAX profile trace to: {profile_dir}", flush=True)
    jax.profiler.start_trace(profile_dir)
    try:
        deadline = time.monotonic() + duration_seconds
        while time.monotonic() < deadline:
            key, subkey = jax.random.split(key)
            x = jax.random.normal(key, (matrix_size, matrix_size))
            y = jax.random.normal(subkey, (matrix_size, matrix_size))
            jnp.dot(x, y).block_until_ready()
    finally:
        jax.profiler.stop_trace()
    print(f"Profile trace written to: {profile_dir}", flush=True)


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    profile_dir = _resolve_profile_dir()
    _run_traced_workload(
        profile_dir,
        _PROFILE_DURATION_SECONDS.value,
        _MATRIX_SIZE.value,
    )

    # Keep the JobSet alive long enough for the Diagon reconciler to create the
    # MLRun from the attached ManagedXprof artifact before the container exits.
    # Configurable via IDLE_SECONDS (default 30 minutes); lower it in CI/smoke
    # runs so the pod does not hold cluster resources longer than necessary.
    idle_seconds = int(os.environ.get("IDLE_SECONDS", str(30 * 60)))
    print(f"Idling for {idle_seconds}s so the JobSet stays alive.", flush=True)
    time.sleep(idle_seconds)

    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
