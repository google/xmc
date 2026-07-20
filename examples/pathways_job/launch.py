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
"""Launch script demonstrating a multi-component Pathways JobSet with ContainerPort mappings.

Runs:
1. `pathways-head` (1 replica with `pathways-rm` and `pathways-proxy` init containers
   exposing ports 29001/29002 and 29000).
2. `worker` (multi-replica TPU worker pods exposing ports 29005, 29006, 8471, and 8080).
"""

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
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

# Skip git push snapshot during local test launch
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def main(argv: Sequence[str]) -> None:
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID")
    image_path = os.environ.get(
        "IMAGE_PATH",
        f"us-docker.pkg.dev/{project_id}/xmc/pathways-workload:latest",
    )
    queue_name = os.environ.get("QUEUE_NAME", "tpu-queue")

    print(f"Creating Pathways multi-component experiment using image: {image_path}")

    with xm_cloud.create_experiment(
        experiment_title="Pathways Multi-Component JobSet Validation",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access

        # Construct the WorkUnit with 2 distinct jobs in the JobSet:
        # 1. pathways-head (control plane with init containers exposing ports 29000, 29001, 29002)
        # 2. worker (multi-replica TPU worker pods exposing ports 29005, 29006, 8471, 8080)
        work_unit = work_unit_pb2.WorkUnit(
            work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
            priority=priority_pb2.Priority.NORMAL,
            notes="Pathways JobSet demonstrating multi-component ports and topology.",
            tags=["pathways", "jobset", "ports", "tpu"],
            executable=work_unit_pb2.Executable(
                scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
                jobset=work_unit_pb2.JobSetExecutable(
                    network=work_unit_pb2.JobSetExecutable.Network(
                        enable_dns_hostnames=True,
                        publish_not_ready_addresses=True,
                    ),
                    jobs=[
                        # Role 1: pathways-head
                        work_unit_pb2.KubernetesJob(
                            name="pathways-head",
                            replicas=1,
                            init_containers=[
                                work_unit_pb2.ExecutableSpec(
                                    name="pathways-rm",
                                    docker_image=image_path,
                                    command=["/bin/pathways_rm"],
                                    ports=[
                                        work_unit_pb2.ContainerPort(
                                            name="rm-1",
                                            container_port=29001,
                                            protocol=work_unit_pb2.ContainerPort.TCP,
                                        ),
                                        work_unit_pb2.ContainerPort(
                                            name="rm-2",
                                            container_port=29002,
                                            protocol=work_unit_pb2.ContainerPort.TCP,
                                        ),
                                    ],
                                    resources=resources_pb2.ResourceSet(
                                        cpu_resources=resources_pb2.CpuResources(
                                            cpu_millicores=1000,
                                            memory_bytes=2048 * 1024 * 1024,
                                        ),
                                    ),
                                ),
                                work_unit_pb2.ExecutableSpec(
                                    name="pathways-proxy",
                                    docker_image=image_path,
                                    command=["/bin/pathways_proxy"],
                                    ports=[
                                        work_unit_pb2.ContainerPort(
                                            name="proxy",
                                            container_port=29000,
                                            protocol=work_unit_pb2.ContainerPort.TCP,
                                        ),
                                    ],
                                    resources=resources_pb2.ResourceSet(
                                        cpu_resources=resources_pb2.CpuResources(
                                            cpu_millicores=1000,
                                            memory_bytes=2048 * 1024 * 1024,
                                        ),
                                    ),
                                ),
                            ],
                            spec=work_unit_pb2.ExecutableSpec(
                                name="jax-tpu",
                                docker_image=image_path,
                                command=["python3", "/app/head_driver.py"],
                                resources=resources_pb2.ResourceSet(
                                    cpu_resources=resources_pb2.CpuResources(
                                        cpu_millicores=2000,
                                        memory_bytes=4096 * 1024 * 1024,
                                    ),
                                ),
                            ),
                        ),
                        # Role 2: worker
                        work_unit_pb2.KubernetesJob(
                            name="worker",
                            replicas=2,
                            spec=work_unit_pb2.ExecutableSpec(
                                name="pathways-worker",
                                docker_image=image_path,
                                command=["python3", "/app/worker.py"],
                                ports=[
                                    work_unit_pb2.ContainerPort(
                                        name="worker",
                                        container_port=29005,
                                        protocol=work_unit_pb2.ContainerPort.TCP,
                                    ),
                                    work_unit_pb2.ContainerPort(
                                        name="data",
                                        container_port=29006,
                                        protocol=work_unit_pb2.ContainerPort.TCP,
                                    ),
                                    work_unit_pb2.ContainerPort(
                                        name="ici",
                                        container_port=8471,
                                        protocol=work_unit_pb2.ContainerPort.TCP,
                                    ),
                                    work_unit_pb2.ContainerPort(
                                        name="metrics",
                                        container_port=8080,
                                        protocol=work_unit_pb2.ContainerPort.TCP,
                                    ),
                                ],
                                resources=resources_pb2.ResourceSet(
                                    tpu_resources=resources_pb2.TpuResources(
                                        type="tpu-v6e-slice",
                                        topology="2x2",
                                    ),
                                ),
                            ),
                        ),
                    ],
                ),
            ),
        )

        create_req = ess_api_pb2.CreateWorkUnitRequest(
            parent=experiment.resource_name,
            work_unit=work_unit,
        )
        created_wu = stub.create_work_unit(create_req)
        print(f"Created Work Unit successfully: {created_wu.name}")
        print(f"Experiment Dashboard URL: https://{os.environ.get('XMC_DOMAIN')}")


if __name__ == "__main__":
    app.run(main)
