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

"""A raw-JAX CPU workload that profiles programmatically, then serves on-demand.

This worker relies solely on the environment variables injected by the XMC
Actuator. It reads the per-work-unit GCS destination from
``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` and uses native ``jax.profiler`` tracing
to write the profile there directly. The Diagon backend then discovers and loads
the profile from that GCS folder using the customized ManagedXprof MLRun labels.

The workload executes on CPUs using JAX.
The first ``--programmatic_steps`` steps (default 10) are captured
programmatically via ``start_trace`` / ``stop_trace``. The remaining steps, up to
``--total_steps`` (default 10000), run against a live JAX profiler server so an
operator can trigger on-demand profile captures (e.g. from the XProf/Diagon UI).
The tail also keeps the enclosing JobSet alive long enough for the Diagon
reconciler to create the MLRun from the attached ManagedXprof artifact.
"""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_PROGRAMMATIC_STEPS = flags.DEFINE_integer(
    "programmatic_steps",
    int(os.environ.get("PROGRAMMATIC_STEPS", "10")),
    "Number of initial steps captured programmatically via start_trace/stop_trace.",
)
_TOTAL_STEPS = flags.DEFINE_integer(
    "total_steps",
    int(os.environ.get("TOTAL_STEPS", "10000")),
    "Total number of steps to run. Steps after --programmatic_steps run against "
    "the live JAX profiler server so they can be captured on demand.",
)
_MATRIX_SIZE = flags.DEFINE_integer(
    "matrix_size",
    int(os.environ.get("MATRIX_SIZE", "4096")),
    "Side length of the square matrices multiplied each step.",
)
_PROFILER_PORT = flags.DEFINE_integer(
    "profiler_port",
    int(os.environ.get("PROFILER_PORT", "9999")),
    "Port the JAX profiler server listens on for on-demand profile captures.",
)

# Environment variable injected by the XMC Actuator into every container.
_XPROF_GCS_FOLDER_ENV = "XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER"


def _resolve_profile_dir() -> str:
    """Resolves the profile destination from the Actuator-injected env var.

    Returns:
      The GCS folder (``gs://...``) that the XMC Actuator injects via
      ``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` for this work unit.

    Raises:
      RuntimeError: if ``XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`` is not set.
    """
    profile_dir = os.environ.get(_XPROF_GCS_FOLDER_ENV)
    if not profile_dir:
        raise RuntimeError(
            f"{_XPROF_GCS_FOLDER_ENV} is not set. This workload must run in a"
            " managed-xprof-configured XMC cluster, where the Actuator injects"
            " the per-work-unit GCS destination folder."
        )
    return profile_dir


@jax.jit
def _matmul_step(x: jnp.ndarray, y: jnp.ndarray) -> jnp.ndarray:
    """JIT-compiled matrix multiplication executed on CPU."""
    return jnp.dot(x, y)


def _run_traced_workload(
    profile_dir: str,
    programmatic_steps: int,
    total_steps: int,
    matrix_size: int,
    profiler_port: int,
) -> None:
    """Runs a stepping workload on CPU: programmatic capture first, then on-demand."""
    devices = jax.devices()
    backend = jax.default_backend()
    print(f"JAX default backend: {backend}", flush=True)
    print(f"Detected JAX devices: {devices}", flush=True)

    key = jax.random.PRNGKey(0)

    # Warm up first so the trace captures steady-state execution rather than
    # one-time JIT compilation.
    print(
        f"Running warm-up matmul ({matrix_size}x{matrix_size}) on {backend}...",
        flush=True,
    )
    key, subkey1, subkey2 = jax.random.split(key, 3)
    warmup = _matmul_step(
        jax.random.normal(subkey1, (matrix_size, matrix_size), dtype=jnp.float32),
        jax.random.normal(subkey2, (matrix_size, matrix_size), dtype=jnp.float32),
    )
    warmup.block_until_ready()
    print("Warm-up completed on accelerator device.", flush=True)

    # Start the profiler server up front so the on-demand phase below can be
    # captured against the live workload.
    jax.profiler.start_server(profiler_port)
    print(f"JAX profiler server started on port {profiler_port}.", flush=True)

    # Phase 1: programmatically profile the first `programmatic_steps` steps.
    print(
        f"Capturing the first {programmatic_steps} steps via programmatic JAX"
        f" profile trace to: {profile_dir}",
        flush=True,
    )
    jax.profiler.start_trace(profile_dir)
    try:
        for _step in range(1, programmatic_steps + 1):
            key, subkey1, subkey2 = jax.random.split(key, 3)
            x = jax.random.normal(subkey1, (matrix_size, matrix_size), dtype=jnp.float32)
            y = jax.random.normal(subkey2, (matrix_size, matrix_size), dtype=jnp.float32)
            res = _matmul_step(x, y)
            res.block_until_ready()
    finally:
        jax.profiler.stop_trace()
    print(f"Programmatic profile written to: {profile_dir}", flush=True)

    # Phase 2: keep running steps so an operator can trigger on-demand profile
    # captures against the live profiler server. This also keeps the JobSet alive
    # long enough for the Diagon reconciler to create the MLRun from the attached
    # ManagedXprof artifact.
    print(
        f"Serving on-demand profiling for steps {programmatic_steps + 1} to"
        f" {total_steps} on port {profiler_port}.",
        flush=True,
    )
    for _step in range(programmatic_steps + 1, total_steps + 1):
        key, subkey1, subkey2 = jax.random.split(key, 3)
        x = jax.random.normal(subkey1, (matrix_size, matrix_size), dtype=jnp.float32)
        y = jax.random.normal(subkey2, (matrix_size, matrix_size), dtype=jnp.float32)
        res = _matmul_step(x, y)
        res.block_until_ready()
        time.sleep(1)


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    profile_dir = _resolve_profile_dir()
    _run_traced_workload(
        profile_dir,
        _PROGRAMMATIC_STEPS.value,
        _TOTAL_STEPS.value,
        _MATRIX_SIZE.value,
        _PROFILER_PORT.value,
    )

    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
