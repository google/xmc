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

"""JAX workload for programmatic and on-demand profiling."""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_PROGRAMMATIC_STEPS = flags.DEFINE_integer(
    "programmatic_steps", 10, "Initial steps traced programmatically."
)
_TOTAL_STEPS = flags.DEFINE_integer("total_steps", 10000, "Total steps to run.")
_MATRIX_SIZE = flags.DEFINE_integer("matrix_size", 4096, "Size of square matrices.")

# ML Diagnostics backend only supports port 9999 so far.
_PROFILER_PORT = 9999


def _resolve_profile_dir() -> str:
    folder = os.environ.get("XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER")
    if not folder:
        raise RuntimeError("XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER environment variable is not set.")
    return folder


@jax.jit
def _matmul_step(x: jnp.ndarray, y: jnp.ndarray) -> jnp.ndarray:
    return jnp.dot(x, y)


def _run_workload(
    profile_dir: str,
    programmatic_steps: int,
    total_steps: int,
    matrix_size: int,
    profiler_port: int = _PROFILER_PORT,
) -> None:
    backend = jax.default_backend()
    print(f"JAX backend: {backend}, devices: {jax.devices()}", flush=True)

    key = jax.random.PRNGKey(0)

    print(f"Warming up ({matrix_size}x{matrix_size})...", flush=True)
    key, subkey1, subkey2 = jax.random.split(key, 3)
    x = jax.random.normal(subkey1, (matrix_size, matrix_size), dtype=jnp.float32)
    y = jax.random.normal(subkey2, (matrix_size, matrix_size), dtype=jnp.float32)
    warmup = _matmul_step(x, y)
    warmup.block_until_ready()
    print("Warmup complete.", flush=True)

    jax.profiler.start_server(profiler_port)
    print(f"JAX profiler server listening on port {profiler_port}.", flush=True)

    print(f"Capturing {programmatic_steps} steps to {profile_dir}...", flush=True)
    jax.profiler.start_trace(profile_dir)
    try:
        for _ in range(1, programmatic_steps + 1):
            _matmul_step(x, y).block_until_ready()
    finally:
        jax.profiler.stop_trace()
    print(f"Programmatic trace written to {profile_dir}", flush=True)

    # Note: Due to an XProf limitation, triggering an on-demand profile capture needs to wait
    # for programmatic tracing to finish here. The 9999 xprof server can be started at the
    # same time, but simultaneous trace captures are not supported.
    print(f"Running steps {programmatic_steps + 1} to {total_steps}...", flush=True)
    for _ in range(programmatic_steps + 1, total_steps + 1):
        _matmul_step(x, y).block_until_ready()
        time.sleep(1)


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    profile_dir = _resolve_profile_dir()
    _run_workload(
        profile_dir,
        _PROGRAMMATIC_STEPS.value,
        _TOTAL_STEPS.value,
        _MATRIX_SIZE.value,
        _PROFILER_PORT,
    )
    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
