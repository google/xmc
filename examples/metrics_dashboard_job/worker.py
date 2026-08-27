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

"""Training worker that logs metrics through the XMC SDK.

The worker talks to the ingestion gateway and nothing else. It holds no database
credentials and never learns where the metrics come to rest: it calls xmc.log_metric,
the SDK buffers and ships to the gateway, and the platform is responsible for landing
the rows the dashboard later reads. Keeping the worker on that side of the boundary is
the point of the example -- a training job should not need a ClickHouse client.
"""

from __future__ import annotations

import math
import os
import random
import time

import xmc
from metric_names import (
    EVAL_ACCURACY,
    EVAL_LOSS,
    STEP_TIME_MS,
    TRAIN_ACCURACY,
    TRAIN_LOSS,
)


def env_value(name: str, default: str) -> str:
    """Reads an environment variable, treating an empty value the same as an unset one.

    A declared-but-blank variable -- NUM_STEPS="" in a manifest or an .env file -- reaches the
    process as '', which int() and float() reject. Blank plainly means "no value given", so it
    takes the default rather than crashing the worker before it logs a single metric.
    """
    return os.environ.get(name, default) or default


def report_ingestion_readiness() -> bool:
    """Prints whether the SDK will actually ship metrics, and returns that verdict.

    The SDK fails closed: with XMC_GATEWAY_URL unset, or any of XMC_XID,
    XMC_WORK_UNIT_ID and XMC_EXECUTION_ID missing, every log_metric call becomes a
    silent no-op. That is the right default for a misconfigured pod, but it is
    indistinguishable from a healthy run in the logs, and the symptom arrives much
    later as a dashboard whose plots are permanently empty. Saying so up front turns a
    silent no-op into an obvious one.
    """
    client = xmc.Client()

    print("--- XMC ingestion configuration ---")
    print(f"  XMC_GATEWAY_URL  : {os.environ.get('XMC_GATEWAY_URL') or '(unset)'}")
    print(f"  XMC_XID          : {client.xid or '(unset)'}")
    print(f"  XMC_WORK_UNIT_ID : {client.work_unit_id or '(unset)'}")
    print(f"  XMC_EXECUTION_ID : {client.execution_id or '(unset)'}")
    print(f"  XMC_JOB_NAME     : {client.job_name or '(unset)'}")
    print(f"  replica          : {client.replica_id}")

    if client.disabled:
        print("!!! XMC_GATEWAY_URL is unset: metrics are DISABLED and will be dropped.")
        return False
    if client.is_silenced:
        print("!!! This replica is silenced: metrics from this process will be dropped.")
        return False

    print("  -> metrics will be sent to the ingestion gateway.")
    return True


def run_training_loop(total_steps: int, seed: int) -> None:
    """Simulates a training run, logging one batch of metrics per step.

    Args:
        total_steps: Number of training steps to simulate.
        seed: Seed for the synthetic noise, so a given experiment replays identically.
    """
    rng = random.Random(seed)
    # Read once rather than per step: the value cannot change mid-loop, and re-reading it inside
    # the hot path only spreads the parse across every iteration.
    step_delay = float(env_value("STEP_DELAY_SECONDS", "0.2"))
    print(f"--> Simulating {total_steps} training steps...")

    for step in range(1, total_steps + 1):
        # Losses decay, accuracies climb, and eval trails train -- the shapes matter
        # only in that they make the resulting charts readable at a glance.
        train_loss = max(0.01, 2.0 * math.exp(-0.03 * step) + rng.uniform(-0.02, 0.02))
        eval_loss = max(0.02, 2.2 * math.exp(-0.025 * step) + rng.uniform(-0.04, 0.04))
        train_accuracy = min(0.999, 0.30 + 0.65 * (1.0 - math.exp(-0.04 * step)))
        eval_accuracy = min(0.999, max(0.0, train_accuracy - rng.uniform(0.01, 0.05)))
        step_time_ms = 120.0 + rng.gauss(0.0, 5.0)

        xmc.log_metric(TRAIN_LOSS, round(train_loss, 4), step=step)
        xmc.log_metric(TRAIN_ACCURACY, round(train_accuracy, 4), step=step)
        xmc.log_metric(EVAL_LOSS, round(eval_loss, 4), step=step)
        xmc.log_metric(EVAL_ACCURACY, round(eval_accuracy, 4), step=step)
        xmc.log_metric(STEP_TIME_MS, round(step_time_ms, 2), step=step)

        if step % 10 == 0 or step == total_steps:
            print(
                f"  [step {step}/{total_steps}] "
                f"{TRAIN_LOSS}={train_loss:.4f} {TRAIN_ACCURACY}={train_accuracy:.4f} "
                f"{EVAL_LOSS}={eval_loss:.4f}"
            )

        time.sleep(step_delay)


def main() -> None:
    """Runs the simulated training loop and flushes the metrics buffer on the way out."""
    print("=== XMC Metrics Dashboard Example Worker Started ===")

    report_ingestion_readiness()

    total_steps = int(env_value("NUM_STEPS", "100"))
    # Seeding from the experiment keeps a rerun of the same experiment reproducible while
    # still giving separate experiments visibly different curves.
    seed = int(env_value("XMC_XID", "0"))

    try:
        run_training_loop(total_steps=total_steps, seed=seed)
    finally:
        # Metrics are buffered, so skipping this drops the tail of the run -- including
        # the final step, which is exactly the value the KPI cards display.
        print("--> Flushing buffered metrics...")
        xmc.shutdown()
        print("=== ✅ Worker Completed ===")


if __name__ == "__main__":
    main()
