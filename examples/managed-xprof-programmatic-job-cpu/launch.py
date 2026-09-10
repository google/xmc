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

"""Launch script for ManagedXprof programmatic profiling on CPU.

This example launches a JAX workload on CPU that captures a profile trace
programmatically. It attaches a work-unit-level ManagedXprof artifact at the
EXECUTION phase so that the Diagon reconciler creates an MLRun tracking record
in Google Cloud Diagon, allowing developers to inspect the profile in the Cloud
Console.
"""

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app, flags
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    artifact_pb2,
    priority_pb2,
    resources_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

_DISPLAY_NAME = flags.DEFINE_string(
    "display_name",
    "managed-xprof-cpu-demo",
    "Display name for the ManagedXprof artifact shown in the Diagon UI.",
)
_RUN_GROUP = flags.DEFINE_string(
    "run_group",
    "managed-xprof-cpu",
    "Run group (tag) for grouping profiling sessions in Diagon.",
)
_NUM_WORK_UNITS = flags.DEFINE_integer(
    "num_work_units",
    1,
    "Number of work units to launch.",
)
_IMAGE_PATH = flags.DEFINE_string(
    "image_path",
    None,
    "Container image path. Defaults to us-docker.pkg.dev/{PROJECT_ID}/xmc/managed-xprof-programmatic-job-cpu:latest.",
)
_QUEUE_NAME = flags.DEFINE_string(
    "queue_name",
    None,
    "Kueue queue name. Defaults to QUEUE_NAME env var or 'cpu-queue'.",
)

# Skip git push snapshot during local test launch so SSH passphrase is not prompted.
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def _build_work_unit(image_path: str, queue_name: str) -> work_unit_pb2.WorkUnit:
    """Builds a single-job WorkUnit running the JAX programmatic profiler workload."""
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="ManagedXprof CPU programmatic profiling workload",
        tags=["diagon", "managed_xprof", "programmatic", "cpu"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    work_unit_pb2.KubernetesJob(
                        name="worker",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="worker",
                            docker_image=image_path,
                            command=["python3", "/app/worker.py"],
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=1000,
                                    memory_bytes=4 * 1024 * 1024 * 1024,
                                ),
                            ),
                        ),
                    ),
                ]
            ),
        ),
    )


def main(argv: Sequence[str]) -> None:
    """Launches the experiment and attaches a ManagedXprof artifact per work unit."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID")
    image_path = _IMAGE_PATH.value or os.environ.get("IMAGE_PATH")
    if not image_path:
        if not project_id:
            raise app.UsageError(
                "PROJECT_ID environment variable must be set when IMAGE_PATH is not specified."
            )
        image_path = f"us-docker.pkg.dev/{project_id}/xmc/managed-xprof-programmatic-job-cpu:latest"

    queue_name = _QUEUE_NAME.value or os.environ.get("QUEUE_NAME", "cpu-queue")

    print(
        "Creating experiment attaching ManagedXprof artifact\n"
        f"  Image: {image_path}\n"
        f"  Queue: {queue_name}\n"
        f"  Work units: {_NUM_WORK_UNITS.value}"
    )

    with xm_cloud.create_experiment(
        experiment_title="Managed XProf Programmatic CPU Example",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = int(experiment.resource_name.split("/")[-1])

        for _ in range(_NUM_WORK_UNITS.value):
            work_unit = _build_work_unit(image_path, queue_name)
            created_wu = stub.create_work_unit(
                ess_api_pb2.CreateWorkUnitRequest(
                    parent=experiment.resource_name,
                    work_unit=work_unit,
                )
            )
            wid = int(created_wu.name.split("/")[3])
            wu_name = f"experiments/{xid}/workUnits/{wid}"
            print(f"Created Work Unit: {wu_name}")

            # Attach ManagedXprof artifact at EXECUTION phase.
            # The Diagon reconciler reads this artifact to create the MLRun tracking record,
            # and the ESS read path enriches it with the resolved Cloud Console URL.
            artifact = artifact_pb2.Artifact(
                tags=["diagon", "managed_xprof"],
                notes="Work-unit ManagedXprof artifact for Diagon MLRun indexing",
                lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
                payload=artifact_pb2.ArtifactPayload(
                    managed_xprof=artifact_pb2.ManagedXprofArtifact(
                        display_name=_DISPLAY_NAME.value,
                        run_group=_RUN_GROUP.value,
                    )
                ),
            )
            created_artifact = stub.create_artifact(
                ess_api_pb2.CreateArtifactRequest(
                    parent=wu_name,
                    artifact=artifact,
                )
            )
            print(f"Attached ManagedXprof artifact: {created_artifact.name}")

        domain = os.environ.get("XMC_DOMAIN", "localhost")
        print(f"\nExperiment Dashboard URL: https://{domain}/experiments/{xid}")


if __name__ == "__main__":
    app.run(main)
