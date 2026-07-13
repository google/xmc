#!/usr/bin/env python3
"""Launch script demonstrating per-job node_selector in an XMC WorkUnit.

Runs a multi-job distributed training simulation:
1. `head` (1 replica): Pinned via `node_selector` to `cloud.google.com/gke-nodepool: cpups`.
2. `worker` (2 replicas): Pinned via `node_selector` to `cloud.google.com/gke-nodepool: gpu-pool`,
   with GKE execution environment automatically injecting `cloud.google.com/gke-accelerator: nvidia-tesla-t4`.
"""

from collections.abc import Sequence
import logging
import os

import dotenv

logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

from absl import app
from xmanager import xm
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code

# Skip git push snapshot during local test launch so SSH passphrase is not prompted
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()

from xmanager_cloud.experiment_state_server.proto import api_pb2 as ess_api_pb2
from xmanager_cloud.experiment_state_server.proto import priority_pb2
from xmanager_cloud.experiment_state_server.proto import resources_pb2
from xmanager_cloud.experiment_state_server.proto import work_unit_pb2
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2


def main(argv: Sequence[str]) -> None:
  if len(argv) > 1:
    raise app.UsageError("Too many command-line arguments.")

  project_id = os.environ.get("PROJECT_ID")
  image_path = os.environ.get(
      "IMAGE_PATH",
      f"us-docker.pkg.dev/{project_id}/xmc/per-job-node-selector:latest",
  )
  queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

  print(f"Creating per-job node_selector experiment using image: {image_path}")

  with xm_cloud.create_experiment(
      experiment_title="Per-Job nodeSelector Validation Experiment",
      acls=xid_messages_pb2.XidAcls(
          owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
      ),
  ) as experiment:
    stub = experiment._experiment_state_stub  # pylint: disable=protected-access

    work_unit = work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="Multi-job JobSet separating head (cpups) and worker (gpu-pool) pods.",
        tags=["node-selector", "cpups", "distributed"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(
                queue_name=queue_name
            ),
            jobset=work_unit_pb2.JobSetExecutable(
                network=work_unit_pb2.JobSetExecutable.Network(
                    enable_dns_hostnames=True,
                    publish_not_ready_addresses=True,
                ),
                jobs=[
                    # Job 1: Head coordinator pod pinned to CPU-only node pool (cpups)
                    work_unit_pb2.KubernetesJob(
                        name="head",
                        replicas=1,
                        node_selector={
                            "cloud.google.com/gke-nodepool": "cpups",
                        },
                        spec=work_unit_pb2.ExecutableSpec(
                            name="coordinator",
                            docker_image=image_path,
                            command=["python3", "/app/coordinator.py"],
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=1000,
                                    memory_bytes=2 * 1024 * 1024 * 1024,
                                ),
                            ),
                            env=[
                                work_unit_pb2.EnvVar(
                                    name="POD_ROLE",
                                    value="head",
                                ),
                                work_unit_pb2.EnvVar(
                                    name="NUM_WORKERS",
                                    value="2",
                                ),
                            ],
                        ),
                    ),
                    # Job 2: Worker pods pinned to GPU pool with T4 accelerator injection
                    work_unit_pb2.KubernetesJob(
                        name="worker",
                        replicas=2,
                        node_selector={
                            "cloud.google.com/gke-nodepool": "gpu-pool",
                        },
                        spec=work_unit_pb2.ExecutableSpec(
                            name="worker-processor",
                            docker_image=image_path,
                            command=["python3", "/app/worker.py"],
                            resources=resources_pb2.ResourceSet(
                                gpu_resources=resources_pb2.GpuResources(
                                    type="T4",
                                    count=1,
                                ),
                            ),
                            env=[
                                work_unit_pb2.EnvVar(
                                    name="POD_ROLE",
                                    value="worker",
                                ),
                                work_unit_pb2.EnvVar(
                                    name="COORDINATOR_ADDR",
                                    value="head-head-0-0",
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
    print(f"Experiment Dashboard URL: https://{os.environ.get('XMC_DOMAIN')}")


if __name__ == "__main__":
  app.run(main)
