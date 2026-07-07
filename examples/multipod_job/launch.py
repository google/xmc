#!/usr/bin/env python3
"""Launch script demonstrating a multi-pod and multi-container (sidecar) XMC WorkUnit.

Runs:
1. `counter-workers` (2 parallel replicas, each with a primary counter container and a sidecar counter container).
2. `secondary-counter` (1 replica running a second distinct pod role in the same WorkUnit).
Leverages the XManager CLI library installed in ./deploy/cli/venv.
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
      f"us-docker.pkg.dev/{project_id}/xmc/multipod-counter:latest",
  )
  queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

  print(f"Creating multi-pod / sidecar experiment using image: {image_path}")

  with xm_cloud.create_experiment(
      experiment_title="Multi-Pod & Sidecar Validation Experiment",
      acls=xid_messages_pb2.XidAcls(
          owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
      ),
  ) as experiment:
    stub = experiment._experiment_state_stub  # pylint: disable=protected-access

    # Construct the WorkUnit with 2 distinct pod roles inside the JobSet:
    # 1. counter-workers (replicas=2) with 1 primary container + 1 sidecar container
    # 2. secondary-counter (replicas=1) with 1 primary container
    work_unit = work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes=(
            "Multi-pod JobSet with 2 worker replicas (primary + sidecar) and a"
            " secondary pod."
        ),
        tags=["multipod", "sidecar", "validation"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(
                queue_name=queue_name
            ),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    # Pod Role 1: 2 parallel replicas, each running primary + sidecar container
                    work_unit_pb2.KubernetesJob(
                        name="counter-workers",
                        replicas=2,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="primary-counter",
                            docker_image=image_path,
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
                                    value="2",
                                ),
                            ],
                        ),
                        sidecars=[
                            work_unit_pb2.ExecutableSpec(
                                name="sidecar-counter",
                                docker_image=image_path,
                                resources=resources_pb2.ResourceSet(
                                    cpu_resources=resources_pb2.CpuResources(
                                        cpu_millicores=50,
                                        memory_bytes=50 * 1024 * 1024,
                                    ),
                                ),
                                env=[
                                    work_unit_pb2.EnvVar(
                                        name="POD_PREFIX",
                                        value="sidecar-worker",
                                    ),
                                    work_unit_pb2.EnvVar(
                                        name="DURATION_MINUTES",
                                        value="2",
                                    ),
                                ],
                            )
                        ],
                    ),
                    # Pod Role 2: A second distinct pod role in the same JobSet
                    work_unit_pb2.KubernetesJob(
                        name="secondary-counter",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="secondary-counter",
                            docker_image=image_path,
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=100,
                                    memory_bytes=100 * 1024 * 1024,
                                ),
                            ),
                            env=[
                                work_unit_pb2.EnvVar(
                                    name="POD_PREFIX",
                                    value="secondary-pod",
                                ),
                                work_unit_pb2.EnvVar(
                                    name="DURATION_MINUTES",
                                    value="2",
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
    print(
        "Experiment Dashboard URL:"
        f" https://{os.environ.get('XMC_DOMAIN')}"
    )


if __name__ == "__main__":
  app.run(main)
