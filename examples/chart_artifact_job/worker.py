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

_DEFAULT_TOTAL_STEPS = 60
_DEFAULT_STEP_DELAY_SECONDS = 1.0


def _read_positive_int(name: str, default: int) -> int:
    """Reads a positive integer environment variable, falling back to a default."""
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    value = int(raw)
    if value < 1:
        raise ValueError(f"{name} must be positive, got {value}")
    return value


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
    total_steps = _read_positive_int("CHART_DEMO_TOTAL_STEPS", _DEFAULT_TOTAL_STEPS)
    seed = _read_positive_int("CHART_DEMO_SEED", random.randrange(1, 1_000_000))

    try:
        run_training_loop(total_steps, seed, _DEFAULT_STEP_DELAY_SECONDS)
    finally:
        print("Flushing remaining buffered metrics...")
        xmc.shutdown()
        print("=== XMC Chart Artifact Worker Completed ===")


if __name__ == "__main__":
    main()
