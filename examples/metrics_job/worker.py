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

"""A training worker script demonstrating XMC Metrics SDK logging."""

import os
import time

import xmc


def run_training_loop(total_steps: int = 10) -> None:
    """Simulates a model training loop while emitting time-series metrics."""
    print(f"Starting simulated training loop for {total_steps} steps...")
    for step in range(1, total_steps + 1):
        loss = 1.0 / step
        accuracy = 0.5 + (0.04 * step)
        learning_rate = 0.01 * (0.9**step)

        print(
            f"[Step {step}/{total_steps}] loss: {loss:.4f}, "
            f"accuracy: {accuracy:.4f}, lr: {learning_rate:.6f}"
        )

        xmc.log_metric("manual/loss", loss, step=step)
        xmc.log_metric("manual/accuracy", accuracy, step=step)
        xmc.log_metric("manual/learning_rate", learning_rate, step=step)

        time.sleep(1.0)


def main() -> None:
    """Entry point for the metrics worker process."""
    print("=== XMC Metrics Example Worker Started ===")
    print(f"XMC_GATEWAY_URL: {os.environ.get('XMC_GATEWAY_URL')}")
    print(f"XMC_XID: {os.environ.get('XMC_XID')}")
    print(f"XMC_WORK_UNIT_ID: {os.environ.get('XMC_WORK_UNIT_ID')}")
    print(f"XMC_EXECUTION_ID: {os.environ.get('XMC_EXECUTION_ID')}")

    try:
        run_training_loop(total_steps=10)
    finally:
        print("Flushing remaining buffered metrics...")
        xmc.shutdown()
        print("=== XMC Metrics Worker Completed ===")


if __name__ == "__main__":
    main()
