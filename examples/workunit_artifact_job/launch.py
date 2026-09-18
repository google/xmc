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

"""Demonstrates creating and verifying WorkUnit artifacts via Experiment State Service.

This example demonstrates the complete WorkUnit artifact lifecycle across all phases:
1. Connecting to the Experiment State Service (ESS).
2. Creating an Experiment (`experiments/{xid}`).
3. Creating a WorkUnit (`experiments/{xid}/workUnits/{wid}`).
4. Constructing and attaching artifacts across INPUT, EXECUTION, and OUTPUT phases
   via `build_input_artifact`, `build_executable_artifact`, and `build_output_artifact`.
5. Reading back and verifying created artifacts via `get_artifact` and `list_artifacts`.
"""

from __future__ import annotations

import logging
import os
from collections.abc import Sequence
from typing import Any

import dotenv
from absl import app, flags
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    artifact_pb2,
    priority_pb2,
    resources_pb2,
    url_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

# Bypass remote git repository push check for standalone launch execution.
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()

FLAGS = flags.FLAGS
flags.DEFINE_string(
    "experiment_title",
    "WorkUnit Artifact Lifecycle Example",
    "Title of the experiment in XMC.",
)
flags.DEFINE_string(
    "queue",
    os.environ.get("QUEUE_NAME", "cpu-queue"),
    "Target scheduling queue for the WorkUnit.",
)


def _resolve_ui_base_url() -> str:
    """Resolves the XMC UI origin URL from environment variables."""
    if ui_url := os.environ.get("XMANAGER_UI_URL", "").strip():
        return ui_url.rstrip("/")
    domain = (
        os.environ.get("XMC_DOMAIN", "").strip()
        or os.environ.get("DOMAIN_NAME", "").strip()
        or "dev.xmanageriso.dev"
    ).rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    os.environ["XMANAGER_UI_URL"] = domain
    return domain


def build_work_unit(queue_name: str) -> work_unit_pb2.WorkUnit:
    """Builds a minimal WorkUnit targeted at the specified queue."""
    image_path = os.environ.get("IMAGE_PATH", "python:3.11-slim")
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="Demo WorkUnit for testing INPUT, EXECUTION, and OUTPUT lifecycle artifacts",
        tags=["artifact-example", queue_name],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    work_unit_pb2.KubernetesJob(
                        name="artifact-demo-worker",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            docker_image=image_path,
                            command=["python3", "-c", "print('WorkUnit artifact demo worker')"],
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=500,
                                    memory_bytes=512 * 1024 * 1024,
                                )
                            ),
                        ),
                    )
                ]
            ),
        ),
    )


def build_input_artifact(
    title: str,
    code_block: str,
    code_url: str,
    notes: str,
) -> artifact_pb2.Artifact:
    """Constructs an INPUT lifecycle phase artifact for a WorkUnit."""
    return artifact_pb2.Artifact(
        tags=["artifact-example", "input"],
        notes=notes,
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.INPUT,
        priority=1,
        url=url_pb2.Url(url=code_url),
        payload=artifact_pb2.ArtifactPayload(
            code_source=artifact_pb2.CodeSourceArtifact(
                title=title,
                description=notes,
                code_block=code_block,
                code_url=code_url,
            )
        ),
    )


def build_executable_artifact(
    title: str,
    log_content: str,
    notes: str,
) -> artifact_pb2.Artifact:
    """Constructs an EXECUTION lifecycle phase artifact for a WorkUnit."""
    return artifact_pb2.Artifact(
        tags=["artifact-example", "execution"],
        notes=notes,
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
        priority=2,
        url=url_pb2.Url(url=f"https://example.com/logs/{title}"),
        payload=artifact_pb2.ArtifactPayload(
            text=artifact_pb2.TextArtifact(
                title=title,
                text=log_content,
            )
        ),
    )


def build_output_artifact(
    url: str,
    display_name: str,
    icon: str,
    notes: str,
) -> artifact_pb2.Artifact:
    """Constructs an OUTPUT lifecycle phase artifact for a WorkUnit."""
    return artifact_pb2.Artifact(
        tags=["artifact-example", "output"],
        notes=notes,
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
        priority=3,
        url=url_pb2.Url(url=url),
        payload=artifact_pb2.ArtifactPayload(
            url=artifact_pb2.UrlArtifact(
                url=url,
                icon=icon,
                display_name=display_name,
            )
        ),
    )


def get_artifact(stub: Any, artifact_name: str) -> artifact_pb2.Artifact:
    """Fetches a single artifact by resource name (`artifacts/{aid}`)."""
    request = ess_api_pb2.GetArtifactRequest(name=artifact_name)
    if hasattr(stub, "get_artifact"):
        return stub.get_artifact(request)
    if hasattr(stub, "GetArtifact"):
        return stub.GetArtifact(request)
    return stub._stub.GetArtifact(request)


def list_work_unit_artifacts(
    stub: Any,
    work_unit_name: str,
) -> list[artifact_pb2.Artifact]:
    """Lists all artifacts attached to a WorkUnit (`experiments/{xid}/workUnits/{wid}`)."""
    request = ess_api_pb2.ListArtifactsRequest(parent=work_unit_name)
    if hasattr(stub, "list_artifacts"):
        response = stub.list_artifacts(request)
    else:
        response = stub.ListArtifacts(request)
    return list(response.artifacts)


def verify_artifacts(
    stub: Any,
    work_unit_name: str,
    created_artifacts: list[artifact_pb2.Artifact],
) -> None:
    """Reads back and verifies created artifacts via get_artifact and list_artifacts."""
    for expected in created_artifacts:
        fetched = get_artifact(stub, expected.name)
        if fetched.name != expected.name:
            raise RuntimeError(
                f"get_artifact mismatch: expected {expected.name}, got {fetched.name}"
            )
        if fetched.lifecycle_phase != expected.lifecycle_phase:
            raise RuntimeError(
                f"get_artifact lifecycle_phase mismatch for {fetched.name}: "
                f"expected {expected.lifecycle_phase}, got {fetched.lifecycle_phase}"
            )
        payload_type = fetched.payload.WhichOneof("type")
        phase_name = artifact_pb2.Artifact.LifecyclePhase.Name(fetched.lifecycle_phase)
        print(
            f"  [✓] Verified via get_artifact: name={fetched.name}, "
            f"phase={phase_name}, priority={fetched.priority}, "
            f"payload={payload_type}, notes={fetched.notes!r}"
        )

    listed = list_work_unit_artifacts(stub, work_unit_name)
    listed_by_name = {artifact.name: artifact for artifact in listed}
    for expected in created_artifacts:
        if expected.name not in listed_by_name:
            raise RuntimeError(f"Artifact {expected.name} missing from list_artifacts response")
    print(
        f"  [✓] Verified via list_artifacts: found all {len(created_artifacts)} "
        f"expected artifacts under {work_unit_name}"
    )


def main(argv: Sequence[str]) -> None:
    """Runs the end-to-end WorkUnit artifact creation and verification flow."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    ui_base_url = _resolve_ui_base_url()
    print(f"🚀 Connecting to Experiment State Service (UI: {ui_base_url})")

    with xm_cloud.create_experiment(
        experiment_title=FLAGS.experiment_title,
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = experiment.resource_name.split("/")[-1]
        print(f"✅ Created Experiment: {experiment.resource_name} (XID: {xid})")

        created_wu = stub.create_work_unit(
            ess_api_pb2.CreateWorkUnitRequest(
                parent=experiment.resource_name,
                work_unit=build_work_unit(FLAGS.queue),
            )
        )
        wu_parts = created_wu.name.split("/")
        wid = wu_parts[3] if len(wu_parts) > 3 and wu_parts[2] == "workUnits" else wu_parts[-1]
        work_unit_name = f"experiments/{xid}/workUnits/{wid}"
        print(f"✅ Created WorkUnit: {work_unit_name}")

        artifacts_to_create = [
            build_input_artifact(
                title="worker.py",
                code_block="def train_step(x, y):\n    return model.fit(x, y)",
                code_url="https://github.com/google/xmc/blob/main/examples/workunit_artifact_job/launch.py",
                notes="Input code source snapshot consumed by the WorkUnit",
            ),
            build_executable_artifact(
                title="worker-0.log",
                log_content=(
                    "[INFO] Step 100/100 completed - loss=0.162 accuracy=0.948\n"
                    "[INFO] Checkpointing completed successfully."
                ),
                notes="Execution logs captured from primary worker container",
            ),
            build_output_artifact(
                url="https://tensorboard.dev/experiment/example-run",
                display_name="TensorBoard Dashboard",
                icon="bar_chart",
                notes="External training metrics dashboard URL",
            ),
        ]

        created_artifacts: list[artifact_pb2.Artifact] = []
        for artifact in artifacts_to_create:
            created = stub.create_artifact(
                ess_api_pb2.CreateArtifactRequest(
                    parent=work_unit_name,
                    artifact=artifact,
                )
            )
            payload_type = created.payload.WhichOneof("type")
            phase_name = artifact_pb2.Artifact.LifecyclePhase.Name(created.lifecycle_phase)
            print(
                f"📎 Attached artifact: {created.name} "
                f"(phase={phase_name}, priority={created.priority}, payload={payload_type}) "
                f"to {work_unit_name}"
            )
            created_artifacts.append(created)

        print("\n🔍 Reading back and verifying artifacts...")
        verify_artifacts(stub, work_unit_name, created_artifacts)
        print("\n==================================================")
        print("  🎉 Experiment and WorkUnit Launch Completed!    ")
        print(f"  Work Unit : {ui_base_url}/{work_unit_name}")
        print(f"  Dashboard : {ui_base_url}/experiments/{xid}")
        print("==================================================\n")


if __name__ == "__main__":
    app.run(main)
