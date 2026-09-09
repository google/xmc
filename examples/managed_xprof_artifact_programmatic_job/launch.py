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

"""Launch script for programmatic profiling with ManagedXprof.

This example demonstrates how to attach a `ManagedXprof` artifact to each
`WorkUnit` using the `CreateArtifact` RPC. The Diagon reconciler reads this
work-unit-level artifact directly from storage to create the corresponding
MLRun tracking record, so no experiment-level artifact is required.

The artifact is attached at the EXECUTION lifecycle phase: the ManagedXprof
artifact is produced during work unit execution, and the ESS read path only
enriches EXECUTION/OUTPUT artifacts with the resolved MLRun URL, which is what
makes the artifact link resolve to the run.

Unlike the `managed_xprof_artifact_job` example (whose worker runs an on-demand
`jax.profiler` server), the worker here captures the profile *programmatically*
using raw JAX and writes it directly to the per-work-unit GCS folder injected by
the XMC Actuator via `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`.
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
_IMAGE_PATH = flags.DEFINE_string(
    "image_path",
    None,
    "Container image to run.",
)
_QUEUE_NAME = flags.DEFINE_string(
    "queue_name",
    None,
    "Scheduling queue name (e.g. tpu-queue or cpu-queue).",
)
_TPU_TYPE = flags.DEFINE_string(
    "tpu_type",
    None,
    "TPU accelerator type (e.g. tpu-v5-lite-podslice). If set, configures TPU resources.",
)
_TPU_TOPOLOGY = flags.DEFINE_string(
    "tpu_topology",
    None,
    "TPU accelerator topology (e.g. 2x2). Required when tpu_type is set.",
)
# Skip git push snapshot during local test launch.
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def _configure_dev_endpoint_if_needed() -> None:
    endpoint = os.environ.get("XMANAGER_ENDPOINT", "")
    if "127.0.0.1" in endpoint or "localhost" in endpoint or os.environ.get("XMC_DEV_AUTH"):
        user_email = os.environ.get("USER_EMAIL", "").strip()
        if not user_email:
            raise ValueError(
                "USER_EMAIL environment variable must be set for dev endpoint authentication."
            )

        from datetime import datetime, timezone

        import grpc
        import jwt
        from xmanager_cloud.experiment_state_server import experiment_state_api
        from xmanager_cloud.experiment_state_server.proto import api_pb2_grpc

        target = endpoint.replace("dns:///", "") if endpoint else "127.0.0.1:50055"
        secret = os.environ.get(
            "JWT_SECRET", "dev-local-jwt-secret-for-south1-testing-32bytes"
        ).encode()

        def dev_create_stub(_: str):
            now = int(datetime.now(timezone.utc).timestamp())
            token = jwt.encode(
                {
                    "email": user_email,
                    "iss": "xmanager-cloud-backend",
                    "aud": "xmanager-cloud-frontend",
                    "iat": now,
                    "exp": now + 86400,
                },
                secret,
                algorithm="HS256",
            )
            channel = grpc.insecure_channel(target)

            class DevAuthInterceptor(grpc.UnaryUnaryClientInterceptor):
                def intercept_unary_unary(self, continuation, client_call_details, request):
                    metadata = list(client_call_details.metadata or [])
                    metadata.append(("authorization", f"Bearer {token}"))
                    metadata.append(("x-goog-user-email", user_email))
                    return continuation(client_call_details._replace(metadata=metadata), request)

            intercepted_channel = grpc.intercept_channel(channel, DevAuthInterceptor())
            grpc.channel_ready_future(intercepted_channel).result(10)
            return api_pb2_grpc.ExperimentStateServiceStub(intercepted_channel), intercepted_channel

        experiment_state_api._create_experiment_state_server_stub = dev_create_stub


def _build_work_unit(
    image_path: str,
    queue_name: str,
    tpu_type: str | None = None,
    tpu_topology: str | None = None,
) -> work_unit_pb2.WorkUnit:
    """Builds a single-job WorkUnit running the JAX profiler workload.

    Args:
      image_path: The container image to run.
      queue_name: The scheduling queue for the work unit.
      tpu_type: Optional TPU accelerator type.
      tpu_topology: Optional TPU accelerator topology.

    Returns:
      A WorkUnit proto ready to be created via the ESS.
    """
    if tpu_type and tpu_topology:
        resources = resources_pb2.ResourceSet(
            tpu_resources=resources_pb2.TpuResources(
                type=tpu_type,
                topology=tpu_topology,
            ),
        )
        matrix_size = "4096"
    else:
        resources = resources_pb2.ResourceSet(
            cpu_resources=resources_pb2.CpuResources(
                cpu_millicores=1000,
                memory_bytes=4 * 1024 * 1024 * 1024,
            ),
        )
        matrix_size = "2000"

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
                        name="programmatic-xprof-worker",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="programmatic-xprof-worker",
                            docker_image=image_path,
                            command=[
                                "python3",
                                "-u",
                                "/app/worker.py",
                                "--programmatic_steps=10",
                                f"--matrix_size={matrix_size}",
                            ],
                            resources=resources,
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
    _configure_dev_endpoint_if_needed()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID")
    if not project_id:
        raise app.UsageError("PROJECT_ID environment variable must be set.")

    tpu_type = _TPU_TYPE.value or os.environ.get("TPU_TYPE")
    tpu_topology = _TPU_TOPOLOGY.value or os.environ.get("TPU_TOPOLOGY")

    image_path = _IMAGE_PATH.value
    if not image_path:
        if tpu_type:
            image_path = f"us-docker.pkg.dev/{project_id}/xmc/programmatic-xprof-tpu-job:latest"
        else:
            image_path = os.environ.get("IMAGE_PATH") or f"us-docker.pkg.dev/{project_id}/xmc/programmatic-xprof-job:latest"

    queue_name = _QUEUE_NAME.value
    if not queue_name:
        queue_name = "tpu-queue" if tpu_type else (os.environ.get("QUEUE_NAME") or "cpu-queue")

    print(
        "Creating experiment attaching a work-unit-level ManagedXprof artifact"
        f" using image: {image_path} on queue: {queue_name}"
    )

    with xm_cloud.create_experiment(
        experiment_title="Programmatic Xprof Example",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        # Extract XID from resource_name (e.g. 'experiments/12345').
        xid = int(experiment.resource_name.split("/")[-1])

        for _ in range(_NUM_WORK_UNITS.value):
            work_unit = _build_work_unit(
                image_path,
                queue_name,
                tpu_type=tpu_type,
                tpu_topology=tpu_topology,
            )
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

        print(f"Experiment Dashboard URL: https://{os.environ.get('XMC_DOMAIN')}/experiments/{xid}")


if __name__ == "__main__":
    app.run(main)
