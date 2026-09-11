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

"""Launch script attaching a work-unit-level ManagedXprof artifact.

This example demonstrates how to attach a `ManagedXprof` artifact to each
`WorkUnit` using the `CreateArtifact` RPC. The Diagon reconciler reads this
work-unit-level artifact directly from storage to create the corresponding
MLRun tracking record, so no experiment-level artifact is required.

The artifact is attached at the EXECUTION lifecycle phase: the ManagedXprof
artifact is produced during work unit execution, and the ESS read path only
enriches EXECUTION/OUTPUT artifacts with the resolved MLRun URL, which is what
makes the artifact link resolve to the run.
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
    "smoke-diagon-display-name",
    "Display name for the ManagedXprof artifact (shown in the XProf UI).",
)
_RUN_GROUP = flags.DEFINE_string(
    "run_group",
    "smoke-diagon-run-group",
    "Run group (tag) for the ManagedXprof profiling session.",
)
_NUM_WORK_UNITS = flags.DEFINE_integer(
    "num_work_units",
    2,
    "Number of work units to launch, each with its own ManagedXprof artifact.",
)
_SLEEP_SECONDS = flags.DEFINE_integer(
    "sleep_seconds",
    300,
    "Seconds for the container job to run so the JobSet stays alive.",
)

# Skip git push snapshot during local test launch.
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def _build_work_unit(
    image_path: str, queue_name: str, sleep_seconds: int
) -> work_unit_pb2.WorkUnit:
    """Builds a single-job WorkUnit running the JAX profiler workload.

    Args:
      image_path: The container image to run.
      queue_name: The scheduling queue for the work unit.
      sleep_seconds: How long the workload should run (passed via env var).

    Returns:
      A WorkUnit proto ready to be created via the ESS.
    """
    env_vars = {}
    if sleep_seconds > 0:
        env_vars["SLEEP_SECONDS"] = str(sleep_seconds)
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="Work unit for testing the ManagedXprof artifact",
        tags=["diagon-e2e", "managed_xprof"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    work_unit_pb2.KubernetesJob(
                        name="managed-xprof-worker",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="managed-xprof-worker",
                            docker_image=image_path,
                            command=["python3", "/app/worker.py"],
                            env_vars=env_vars,
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
    """Launches an experiment and attaches a ManagedXprof artifact per work unit."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID")
    image_path = os.environ.get("IMAGE_PATH")
    if not image_path:
        if not project_id:
            raise app.UsageError(
                "PROJECT_ID environment variable must be set when IMAGE_PATH is not specified."
            )
        image_path = f"us-docker.pkg.dev/{project_id}/xmc/managed-xprof-artifact-job:latest"
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

    print(
        "Creating experiment attaching a work-unit-level ManagedXprof artifact"
        f" using image: {image_path}"
    )

    domain = (os.environ.get("XMC_DOMAIN", "").strip() or "dev.xmanageriso.dev").rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    ui_base_url = os.environ.get("XMANAGER_UI_URL", "").strip() or domain
    if not ui_base_url.startswith(("http://", "https://")):
        ui_base_url = f"https://{ui_base_url}"
    ui_base_url = ui_base_url.rstrip("/")
    os.environ["XMANAGER_UI_URL"] = ui_base_url

    with xm_cloud.create_experiment(
        experiment_title="ManagedXprof WorkUnit Artifact Example",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        # Extract XID from resource_name (e.g. 'experiments/12345').
        xid = int(experiment.resource_name.split("/")[-1])

        for _ in range(_NUM_WORK_UNITS.value):
            work_unit = _build_work_unit(image_path, queue_name, _SLEEP_SECONDS.value)
            created_wu = stub.create_work_unit(
                ess_api_pb2.CreateWorkUnitRequest(
                    parent=experiment.resource_name,
                    work_unit=work_unit,
                )
            )
            # created_wu is an Operation named
            # experiments/{xid}/workUnits/{wid}/operations/{op_id}.
            wid = int(created_wu.name.split("/")[3])
            wu_name = f"experiments/{xid}/workUnits/{wid}"
            print(f"Created Work Unit successfully: {wu_name}")

            # The ManagedXprof artifact is produced during work unit execution,
            # so it belongs to the EXECUTION phase. The reconciler reads this
            # artifact from storage to create the MLRun, and the ESS read path
            # enriches it with the resolved MLRun URL.
            artifact = artifact_pb2.Artifact(
                tags=["diagon-e2e", "managed_xprof"],
                notes="Work-unit-level ManagedXprof artifact for MLRun creation",
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
            print(f"Attached work-unit-level ManagedXprof artifact: {created_artifact.name}")

        print(f"Experiment Dashboard URL: {ui_base_url}/experiments/{xid}")


if __name__ == "__main__":
    app.run(main)
