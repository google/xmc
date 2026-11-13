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

"""Launch script demonstrating an XMC WorkUnit with both Init and Sidecar Containers."""

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    priority_pb2,
    resources_pb2,
    work_unit_pb2,
)

# Skip git push snapshot during local test launch
snapshot_code.push_repo_state = lambda *_args, **_kwargs: type("Dummy", (), {"commit_url": None})()


def main(argv: Sequence[str]) -> None:
    """Launches sidecar and init container validation experiment."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID")
    image_path = os.environ.get(
        "IMAGE_PATH",
        f"us-docker.pkg.dev/{project_id}/xmc/sidecar-job:latest",
    )
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

    print(f"Creating sidecar and init container validation experiment using image: {image_path}")

    with xm_cloud.create_experiment(
        experiment_title="Sidecar & Init Container Validation Experiment",
        # No `acls`: XMC makes you (the creator) the owner. See docs/authentication.md.
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access

        work_unit = work_unit_pb2.WorkUnit(
            work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
            priority=priority_pb2.Priority.NORMAL,
            notes="JobSet demonstrating Init Container and Kubernetes Native Sidecar Container.",
            tags=["sidecar", "init-container", "validation"],
            executable=work_unit_pb2.Executable(
                scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
                jobset=work_unit_pb2.JobSetExecutable(
                    jobs=[
                        work_unit_pb2.KubernetesJob(
                            name="sidecar-worker",
                            replicas=1,
                            init_containers=[
                                work_unit_pb2.ExecutableSpec(
                                    name="init-setup",
                                    docker_image=image_path,
                                    command=["python3", "/app/init_step.py"],
                                    resources=resources_pb2.ResourceSet(
                                        cpu_resources=resources_pb2.CpuResources(
                                            cpu_millicores=50,
                                            memory_bytes=50 * 1024 * 1024,
                                        ),
                                    ),
                                    env=[
                                        work_unit_pb2.EnvVar(
                                            name="POD_PREFIX",
                                            value="init-setup",
                                        ),
                                    ],
                                )
                            ],
                            sidecars=[
                                work_unit_pb2.ExecutableSpec(
                                    name="sidecar-service",
                                    docker_image=image_path,
                                    command=["python3", "/app/sidecar.py"],
                                    resources=resources_pb2.ResourceSet(
                                        cpu_resources=resources_pb2.CpuResources(
                                            cpu_millicores=50,
                                            memory_bytes=50 * 1024 * 1024,
                                        ),
                                    ),
                                    env=[
                                        work_unit_pb2.EnvVar(
                                            name="POD_PREFIX",
                                            value="sidecar-service",
                                        ),
                                    ],
                                )
                            ],
                            spec=work_unit_pb2.ExecutableSpec(
                                name="primary-worker",
                                docker_image=image_path,
                                command=["python3", "/app/worker.py"],
                                resources=resources_pb2.ResourceSet(
                                    cpu_resources=resources_pb2.CpuResources(
                                        cpu_millicores=100,
                                        memory_bytes=100 * 1024 * 1024,
                                    ),
                                ),
                                env=[
                                    work_unit_pb2.EnvVar(
                                        name="POD_PREFIX",
                                        value="primary-worker",
                                    ),
                                    work_unit_pb2.EnvVar(
                                        name="DURATION_MINUTES",
                                        value="1",
                                    ),
                                ],
                            ),
                        ),
                    ]
                ),
            ),
        )

        create_req = ess_api_pb2.CreateWorkUnitRequest(
            parent=experiment.resource_name,
            work_unit=work_unit,
        )
        created_wu = stub.create_work_unit(create_req)
        print(f"Created Work Unit successfully: {created_wu.name}")
        print(f"Experiment Dashboard URL: https://{os.environ.get('XMC_DOMAIN', 'localhost')}")


if __name__ == "__main__":
    app.run(main)
