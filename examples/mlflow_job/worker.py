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

"""Training worker using only standard MLflow calls.

XMC injects MLFLOW_TRACKING_URI, MLFLOW_EXPERIMENT_ID, MLFLOW_RUN_ID, and any
configured MLFLOW_TRACKING_* credentials into the pod environment, so standard
`mlflow.start_run()` resumes the pre-created child run for this work unit.
"""

from __future__ import annotations

import argparse
import os
import time

import mlflow


def parse_args() -> argparse.Namespace:
    """Parses worker hyperparameters and loop settings."""
    parser = argparse.ArgumentParser(description="Standard MLflow training worker.")
    parser.add_argument("--lr", type=float, default=0.01, help="Learning rate.")
    parser.add_argument("--seed", type=int, default=1, help="Random seed.")
    parser.add_argument("--steps", type=int, default=10, help="Number of training steps.")
    parser.add_argument(
        "--step_sleep_sec",
        type=float,
        default=1.0,
        help="Seconds to sleep between logged steps.",
    )
    return parser.parse_args()


def run_training(lr: float, seed: int, steps: int, step_sleep_sec: float) -> None:
    """Logs hyperparameters and a synthetic metric curve to MLflow."""
    with mlflow.start_run():
        mlflow.log_params({"learning_rate": lr, "seed": seed})
        seed_offset = (seed % 5) * 0.02
        for step in range(1, steps + 1):
            progress = step / max(1, steps)
            loss = max(0.05, (1.0 - 0.7 * progress) * (1.0 + lr * 5.0) + seed_offset)
            accuracy = min(0.99, 0.4 + 0.5 * progress - seed_offset)
            mlflow.log_metric("loss", loss, step=step)
            mlflow.log_metric("accuracy", accuracy, step=step)
            print(
                f"[step {step}/{steps}] lr={lr} seed={seed} loss={loss:.4f} accuracy={accuracy:.4f}",
                flush=True,
            )
            if step_sleep_sec > 0:
                time.sleep(step_sleep_sec)


def main() -> None:
    """Entry point for the MLflow worker pod."""
    args = parse_args()
    print("=== XMC MLflow Example Worker Started ===", flush=True)
    print(f"MLFLOW_TRACKING_URI={os.environ.get('MLFLOW_TRACKING_URI', '')}", flush=True)
    print(f"MLFLOW_EXPERIMENT_ID={os.environ.get('MLFLOW_EXPERIMENT_ID', '')}", flush=True)
    print(f"MLFLOW_RUN_ID={os.environ.get('MLFLOW_RUN_ID', '')}", flush=True)
    run_training(args.lr, args.seed, args.steps, args.step_sleep_sec)
    print("=== XMC MLflow Example Worker Completed ===", flush=True)


if __name__ == "__main__":
    main()
