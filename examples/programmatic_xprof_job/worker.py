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

The worker captures the first ``--programmatic_steps`` steps (default 50) this
way. It then starts a JAX profiler server and keeps running matrix
multiplications for ``IDLE_SECONDS`` (default 30 minutes), so the following steps
are available for on-demand profile captures and the enclosing JobSet stays alive
long enough for the Diagon reconciler to create the MLRun from the attached
ManagedXprof artifact.
"""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_PROGRAMMATIC_STEPS = flags.DEFINE_integer(
    "programmatic_steps",
    50,
    "Number of steps captured programmatically via start_trace/stop_trace before "
    "the worker switches to serving the on-demand profiler port.",
)
_MATRIX_SIZE = flags.DEFINE_integer(
    "matrix_size",
    5000,
    "Side length of the square matrices multiplied during profiling.",
)
_PROFILER_PORT = flags.DEFINE_integer(
    "profiler_port",
    9999,
    "Port the JAX profiler server listens on for on-demand profile captures.",
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


def _run_traced_workload(profile_dir: str, num_steps: int, matrix_size: int) -> None:
    """Captures the first ``num_steps`` steps via programmatic JAX tracing.

    Args:
      profile_dir: Destination directory (local path or ``gs://`` URL) that the
        JAX profiler writes the captured trace to.
      num_steps: Number of matmul steps to run while the trace is active. Only
        these initial steps are captured programmatically; the caller keeps the
        workload running afterwards to serve on-demand captures.
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

    print(
        f"Capturing the first {num_steps} steps via programmatic JAX profile"
        f" trace to: {profile_dir}",
        flush=True,
    )
    jax.profiler.start_trace(profile_dir)
    try:
        for _ in range(num_steps):
            key, subkey = jax.random.split(key)
            x = jax.random.normal(key, (matrix_size, matrix_size))
            y = jax.random.normal(subkey, (matrix_size, matrix_size))
            jnp.dot(x, y).block_until_ready()
    finally:
        jax.profiler.stop_trace()
    print(f"Profile trace written to: {profile_dir} ({num_steps} steps)", flush=True)


def _serve_for_on_demand_profiling(
    matrix_size: int, duration_seconds: int, profiler_port: int
) -> None:
    """Keeps the workload alive and profileable for on-demand capture.

    Starts a JAX profiler server and runs continuous matrix multiplications for
    ``duration_seconds`` so that, in addition to the profile captured
    programmatically above, an operator can trigger an on-demand profile capture
    (e.g. from the XProf/Diagon UI) against the live workload during this window.
    Running real computation -- rather than sleeping -- ensures those on-demand
    captures record representative work. The window also keeps the enclosing
    JobSet alive long enough for the Diagon reconciler to create the MLRun from
    the attached ManagedXprof artifact.

    Args:
      matrix_size: Side length of the square matrices multiplied each cycle.
      duration_seconds: Wall-clock time to keep serving before exiting.
      profiler_port: Port the JAX profiler server listens on for on-demand
        capture requests.
    """
    jax.profiler.start_server(profiler_port)
    print(
        f"JAX profiler server started on port {profiler_port}; workload is"
        f" available for on-demand profiling for {duration_seconds}s.",
        flush=True,
    )
    key = jax.random.PRNGKey(0)
    deadline = time.monotonic() + duration_seconds
    while time.monotonic() < deadline:
        key, subkey = jax.random.split(key)
        x = jax.random.normal(key, (matrix_size, matrix_size))
        y = jax.random.normal(subkey, (matrix_size, matrix_size))
        jnp.dot(x, y).block_until_ready()
        time.sleep(1)


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    profile_dir = _resolve_profile_dir()
    _run_traced_workload(
        profile_dir,
        _PROGRAMMATIC_STEPS.value,
        _MATRIX_SIZE.value,
    )

    # After the programmatic trace, keep the workload alive AND profileable so an
    # operator can also trigger on-demand profile captures against the live JAX
    # profiler server. This also keeps the JobSet alive long enough for the Diagon
    # reconciler to create the MLRun from the attached ManagedXprof artifact.
    # Configurable via IDLE_SECONDS (default 30 minutes); lower it in CI/smoke
    # runs so the pod does not hold cluster resources longer than necessary.
    idle_seconds = int(os.environ.get("IDLE_SECONDS", str(30 * 60)))
    _serve_for_on_demand_profiling(
        _MATRIX_SIZE.value, idle_seconds, _PROFILER_PORT.value
    )

    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
