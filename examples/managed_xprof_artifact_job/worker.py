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

"""A simple CPU JAX workload that runs a profiler server for ManagedXprof.

The workload starts a JAX profiler server and repeatedly runs matrix
multiplications so that a profile can be captured. It runs for `SLEEP_SECONDS`
seconds (read from the environment) so the enclosing JobSet stays alive long
enough for the reconciler to create the MLRun from the attached ManagedXprof
artifact, then exits cleanly.
"""

import os
import time

import jax
import jax.numpy as jnp
import jax.profiler
from absl import app, flags

_PROFILER_PORT = flags.DEFINE_integer("profiler_port", 9999, "Port for the JAX profiler server.")


def main(argv):
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    run_seconds = int(os.environ.get("SLEEP_SECONDS", "300"))

    jax.profiler.start_server(_PROFILER_PORT.value)
    print(f"JAX profiler server started on port {_PROFILER_PORT.value}", flush=True)

    key = jax.random.PRNGKey(0)
    deadline = time.time() + run_seconds
    while time.time() < deadline:
        key, subkey = jax.random.split(key)
        x = jax.random.normal(key, (5000, 5000))
        y = jax.random.normal(subkey, (5000, 5000))
        result = jnp.dot(x, y)
        result.block_until_ready()
        print("Completed a JAX computation cycle.", flush=True)
        time.sleep(1)

    print("Workload finished successfully.", flush=True)


if __name__ == "__main__":
    app.run(main)
