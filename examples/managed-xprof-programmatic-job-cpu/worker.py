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

"""A simple JAX CPU workload demonstrating programmatic profiling.

This worker captures a JAX profile trace programmatically using
`jax.profiler.start_trace()` and `jax.profiler.stop_trace()`.

The profile destination is read from the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`
environment variable, which is automatically injected into the container by the
XMC Actuator. The Diagon reconciler reads the attached ManagedXprof artifact and
indexes the captured profile into an MLRun in Google Cloud Diagon.
"""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_STEPS = flags.DEFINE_integer(
    "steps",
    int(os.environ.get("STEPS", "20")),
    "Number of computational steps to profile programmatically.",
)
_MATRIX_SIZE = flags.DEFINE_integer(
    "matrix_size",
    int(os.environ.get("MATRIX_SIZE", "2048")),
    "Size of square matrices multiplied during each step.",
)
_SLEEP_SECONDS = flags.DEFINE_integer(
    "sleep_seconds",
    int(os.environ.get("SLEEP_SECONDS", "180")),
    "Seconds to keep the container alive after profiling for MLRun reconciliation.",
)

_XPROF_GCS_FOLDER_ENV = "XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER"


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    profile_dir = os.environ.get(_XPROF_GCS_FOLDER_ENV)
    if not profile_dir:
        raise RuntimeError(
            f"{_XPROF_GCS_FOLDER_ENV} is not set. This workload must run in an"
            " XMC cluster with managed ML diagnostics enabled."
        )

    print(f"JAX backend: {jax.default_backend()}", flush=True)
    print(f"Capturing {_STEPS.value} steps programmatically to: {profile_dir}", flush=True)

    # Capture programmatic profile trace
    jax.profiler.start_trace(profile_dir)
    try:
        key = jax.random.PRNGKey(0)
        n = _MATRIX_SIZE.value
        for step in range(1, _STEPS.value + 1):
            key, subkey = jax.random.split(key)
            x = jax.random.normal(key, (n, n), dtype=jnp.float32)
            y = jax.random.normal(subkey, (n, n), dtype=jnp.float32)
            result = jnp.dot(x, y)
            result.block_until_ready()
            if step % 5 == 0 or step == _STEPS.value:
                print(f"Step {step}/{_STEPS.value} completed.", flush=True)
    finally:
        jax.profiler.stop_trace()

    print(f"Profile trace successfully written to: {profile_dir}", flush=True)

    # Keep container alive briefly so Diagon reconciler indexes the MLRun
    if _SLEEP_SECONDS.value > 0:
        print(
            f"Keeping container alive for {_SLEEP_SECONDS.value}s for Diagon MLRun indexing...",
            flush=True,
        )
        time.sleep(_SLEEP_SECONDS.value)

    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
