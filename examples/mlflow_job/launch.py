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

"""Launch script for a 2x2 hyperparameter sweep with no MLflow-specific code.

When the `mlflow` component is enabled on the XMC cluster, XMC automatically
creates an MLflow experiment, a parent run for this launch, and one child run
per WorkUnit, and injects the tracking environment variables into every pod.

Usage:
    python3 launch.py --queue=cpu-queue
"""

from __future__ import annotations

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app, flags
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    priority_pb2,
    resources_pb2,
    work_unit_pb2,
)

logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

FLAGS = flags.FLAGS
flags.DEFINE_string(
    "queue",
    os.environ.get("QUEUE_NAME", "cpu-queue"),
    "Target Kueue LocalQueue name.",
)
flags.DEFINE_string(
    "learning_rates",
    "0.01,0.001",
    "Comma-separated learning rates to sweep.",
)
flags.DEFINE_string(
    "seeds",
    "1,2",
    "Comma-separated random seeds to sweep.",
)
flags.DEFINE_integer("steps", 10, "Training steps per WorkUnit.")
flags.DEFINE_float("step_sleep_sec", 1.0, "Seconds to sleep per training step.")
flags.DEFINE_string(
    "experiment_title",
    "MLflow Sweep Example",
    "Title for the XMC experiment (and matching MLflow experiment).",
)

# Skip git push snapshot during example launch so SSH passphrase is not prompted.
snapshot_code.push_repo_state = lambda *_args, **_kwargs: type("Dummy", (), {"commit_url": None})()


def _parse_csv_items(raw: str) -> list[str]:
    """Splits a comma-separated flag value into stripped non-empty items."""
    items = [item.strip() for item in raw.split(",") if item.strip()]
    if not items:
        raise ValueError("Expected at least one value.")
    return items


def _build_work_unit(
    queue_name: str,
    image_path: str,
    lr: str,
    seed: str,
    steps: int,
    step_sleep_sec: float,
) -> work_unit_pb2.WorkUnit:
    """Constructs one STANDARD WorkUnit for a (lr, seed) trial."""
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes=f"MLflow sweep trial lr={lr}, seed={seed}",
        tags=["mlflow-example", f"lr-{lr}", f"seed-{seed}"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    work_unit_pb2.KubernetesJob(
                        name="trainer",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="trainer",
                            docker_image=image_path,
                            command=[
                                "python3",
                                "/app/worker.py",
                                f"--lr={lr}",
                                f"--seed={seed}",
                                f"--steps={steps}",
                                f"--step_sleep_sec={step_sleep_sec}",
                            ],
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=500,
                                    memory_bytes=512 * 1024 * 1024,
                                ),
                            ),
                        ),
                    )
                ]
            ),
        ),
    )


def main(argv: Sequence[str]) -> None:
    """Submits a multi-WorkUnit sweep experiment to XMC."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID") or os.environ.get("GCP_PROJECT_ID") or "my-project"
    image_path = os.environ.get(
        "IMAGE_PATH",
        f"us-docker.pkg.dev/{project_id}/xmc/mlflow-job:latest",
    )
    learning_rates = _parse_csv_items(FLAGS.learning_rates)
    seeds = _parse_csv_items(FLAGS.seeds)

    print(f"Launching '{FLAGS.experiment_title}' using image: {image_path}")
    with xm_cloud.create_experiment(experiment_title=FLAGS.experiment_title) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = int(experiment.resource_name.split("/")[-1])
        for lr in learning_rates:
            for seed in seeds:
                wu = _build_work_unit(
                    queue_name=FLAGS.queue,
                    image_path=image_path,
                    lr=lr,
                    seed=seed,
                    steps=FLAGS.steps,
                    step_sleep_sec=FLAGS.step_sleep_sec,
                )
                created = stub.create_work_unit(
                    ess_api_pb2.CreateWorkUnitRequest(
                        parent=experiment.resource_name,
                        work_unit=wu,
                    )
                )
                print(f"Created WorkUnit {created.name} (lr={lr}, seed={seed})")
        print(f"Experiment XID: {xid}")


if __name__ == "__main__":
    app.run(main)
