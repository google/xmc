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

"""Worker that streams randomized training metrics into ClickHouse via the XMC SDK.

`xmc.log_metric` posts to the Ingestion Gateway, which publishes to Redpanda; the
ClickHouse materialized view then lands the rows in `default.xmc_metrics`. The Chart
provisioned by `launch.py` reads those rows back through the DataEngine.
"""

import os
import random
import time

import xmc
from training_metrics import METRIC_NAMES, sample_step_metrics

_DEFAULT_TOTAL_STEPS = "60"
_DEFAULT_STEP_DELAY_SECONDS = 1.0


def env_value(name: str, default: str) -> str:
    """Reads an environment variable, treating an empty value the same as an unset one.

    A declared-but-blank variable -- CHART_DEMO_TOTAL_STEPS="" in a manifest or an .env file --
    reaches the process as '', which int() rejects. Blank plainly means "no value given", so it
    takes the default rather than crashing the worker before it logs a single metric.
    """
    return os.environ.get(name, default) or default


def positive_int_env(name: str, default: str) -> int:
    """Reads a positive integer setting, refusing a malformed or out-of-range value.

    Falling back to the default on a bad value is the worse failure: a worker that quietly runs
    60 steps when the manifest asked for 5000 is indistinguishable from a healthy run. Naming
    the variable and the value it was given saves the operator from reading a traceback.
    """
    raw = env_value(name, default)
    try:
        value = int(raw)
    except ValueError:
        raise ValueError(f"{name}={raw!r} is not an integer") from None
    if value <= 0:
        raise ValueError(f"{name}={raw!r} must be greater than zero")
    return value


def seed_env(name: str, default: str) -> int:
    """Reads the RNG seed, warning and falling back when it is not an integer.

    Unlike the setting above, a wrong seed costs nothing -- it only picks which synthetic noise
    the run replays -- and zero and negatives are perfectly good seeds, so nothing is range
    checked here. Refusing to start over a value the sampler itself tolerates would trade a
    working run for a cosmetic one.
    """
    raw = env_value(name, default)
    try:
        return int(raw)
    except ValueError:
        print(f"!!! {name}={raw!r} is not an integer: seeding from {default} instead.")
        return int(default)


def run_training_loop(total_steps: int, seed: int, step_delay_seconds: float) -> None:
    """Emits one randomized sample per step for every metric in METRIC_NAMES.

    Args:
        total_steps: Number of steps to simulate.
        seed: Seed for the sampler, so a run can be reproduced.
        step_delay_seconds: Pause between steps, spreading points over the time axis.
    """
    rng = random.Random(seed)
    print(f"Simulating {total_steps} training steps (seed={seed})...")

    for step in range(1, total_steps + 1):
        sample = sample_step_metrics(step, total_steps, rng)
        for metric_name in METRIC_NAMES:
            xmc.log_metric(metric_name, sample[metric_name], step=step)

        rendered = ", ".join(f"{name}={sample[name]:.4f}" for name in METRIC_NAMES)
        print(f"[Step {step}/{total_steps}] {rendered}")
        time.sleep(step_delay_seconds)


def main() -> None:
    """Entry point for the chart artifact metrics worker."""
    print("=== XMC Chart Artifact Worker Started ===")
    print(f"XMC_GATEWAY_URL: {os.environ.get('XMC_GATEWAY_URL')}")
    print(f"XMC_XID: {os.environ.get('XMC_XID')}")
    print(f"XMC_WORK_UNIT_ID: {os.environ.get('XMC_WORK_UNIT_ID')}")
    print(f"XMC_EXECUTION_ID: {os.environ.get('XMC_EXECUTION_ID')}")

    # launch.py always forwards CHART_DEMO_SEED, drawing one if it was not configured, so
    # that the seed is printed by the launcher. The fallback only covers a manual run.
    total_steps = positive_int_env("CHART_DEMO_TOTAL_STEPS", _DEFAULT_TOTAL_STEPS)
    seed = seed_env("CHART_DEMO_SEED", str(random.randrange(1, 1_000_000)))

    try:
        run_training_loop(total_steps, seed, _DEFAULT_STEP_DELAY_SECONDS)
    finally:
        print("Flushing remaining buffered metrics...")
        xmc.shutdown()
        print("=== XMC Chart Artifact Worker Completed ===")


if __name__ == "__main__":
    main()
