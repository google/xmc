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

"""Simplified launcher for XMC + Diagon Profiling."""

import argparse
import logging
import os

import dotenv
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

# Bypass remote git repository check for standalone launch execution
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def _build_work_unit(
    image_path: str,
    queue_name: str,
    programmatic_steps: int = 10,
    total_steps: int = 10000,
    matrix_size: int = 2000,
) -> work_unit_pb2.WorkUnit:
    """Builds a single-job WorkUnit running the JAX profiler workload."""
    resources = resources_pb2.ResourceSet(
        cpu_resources=resources_pb2.CpuResources(
            cpu_millicores=1000,
            memory_bytes=4 * 1024 * 1024 * 1024,
        ),
    )
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="Work unit for testing ManagedXprof artifact",
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
                                f"--programmatic_steps={programmatic_steps}",
                                f"--total_steps={total_steps}",
                                f"--matrix_size={matrix_size}",
                            ],
                            resources=resources,
                        ),
                    ),
                ]
            ),
        ),
    )


def _build_arg_parser(default_image: str | None) -> argparse.ArgumentParser:
    """Constructs the CLI argument parser for ManagedXprof experiment launch."""
    parser = argparse.ArgumentParser(description="Launch XMC + Diagon Profiling Experiment")
    parser.add_argument(
        "--experiment_name",
        "--experiment_title",
        default="ManagedXprof WorkUnit Artifact Example",
        help="Title of the experiment in XMC UI",
    )
    parser.add_argument("--display_name", default="", help="UI display name for ManagedXprof")
    parser.add_argument("--run_group", default="", help="Run group for ManagedXprof session")
    parser.add_argument("--num_work_units", type=int, default=1, help="Number of work units")
    parser.add_argument("--image_path", default=default_image, help="Container image")
    parser.add_argument(
        "--queue_name",
        default=os.environ.get("QUEUE_NAME", "cpu-queue"),
        help="Scheduling queue (default: cpu-queue)",
    )
    parser.add_argument("--programmatic_steps", type=int, default=10, help="Initial traced steps")
    parser.add_argument("--total_steps", type=int, default=10000, help="Total steps to run")
    parser.add_argument("--matrix_size", type=int, default=2000, help="Matrix dimension")
    return parser


def _resolve_ui_base_url() -> str:
    """Resolves and normalizes XMANAGER_UI_URL from environment variables."""
    domain = (os.environ.get("XMC_DOMAIN", "").strip() or "example.com").rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    ui_base_url = os.environ.get("XMANAGER_UI_URL", "").strip() or domain
    if not ui_base_url.startswith(("http://", "https://")):
        ui_base_url = f"https://{ui_base_url}"
    ui_base_url = ui_base_url.rstrip("/")
    os.environ["XMANAGER_UI_URL"] = ui_base_url
    return ui_base_url


def _create_work_unit_with_xprof_artifact(
    stub: object,
    experiment_resource_name: str,
    xid: int,
    args: argparse.Namespace,
) -> None:
    """Creates a WorkUnit and attaches its EXECUTION-phase ManagedXprof artifact."""
    work_unit = _build_work_unit(
        args.image_path,
        args.queue_name,
        programmatic_steps=args.programmatic_steps,
        total_steps=args.total_steps,
        matrix_size=args.matrix_size,
    )
    created_wu = stub.create_work_unit(  # type: ignore[attr-defined]
        ess_api_pb2.CreateWorkUnitRequest(parent=experiment_resource_name, work_unit=work_unit)
    )
    parts = created_wu.name.split("/")
    wid = parts[3] if len(parts) >= 4 and parts[2] == "workUnits" else parts[-1]
    wu_name = f"experiments/{xid}/workUnits/{wid}"
    print(f"✅ Created Work Unit: {wu_name}")

    artifact = artifact_pb2.Artifact(
        tags=["diagon-e2e", "managed_xprof"],
        notes="Work-unit-level ManagedXprof artifact for MLRun creation",
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
        payload=artifact_pb2.ArtifactPayload(
            managed_xprof=artifact_pb2.ManagedXprofArtifact(
                display_name=args.display_name or f"{args.experiment_name}_{wid}",
                run_group=args.run_group or args.experiment_name,
            )
        ),
    )
    created_artifact = stub.create_artifact(  # type: ignore[attr-defined]
        ess_api_pb2.CreateArtifactRequest(parent=wu_name, artifact=artifact)
    )
    print(f"📎 Attached ManagedXprof artifact: {created_artifact.name}")


def main() -> None:
    """Launches the ManagedXprof experiment and attaches profiling artifacts."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    project_id = os.environ.get("PROJECT_ID")
    default_image = os.environ.get("IMAGE_PATH") or (
        f"us-docker.pkg.dev/{project_id}/xmc/programmatic-xprof-job:latest" if project_id else None
    )
    parser = _build_arg_parser(default_image)
    args = parser.parse_args()
    if not args.image_path:
        parser.error("PROJECT_ID environment variable must be set when --image_path is omitted.")

    ui_base_url = _resolve_ui_base_url()
    print(f"🚀 Launching experiment '{args.experiment_name}' (Image: {args.image_path})")
    acls = xid_messages_pb2.XidAcls(
        owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
    )
    with xm_cloud.create_experiment(experiment_title=args.experiment_name, acls=acls) as exp:
        xid = int(exp.resource_name.split("/")[-1])
        for _ in range(args.num_work_units):
            _create_work_unit_with_xprof_artifact(
                exp._experiment_state_stub, exp.resource_name, xid, args
            )
        print(f"\n🎉 Success! View Experiment Dashboard:\n👉 {ui_base_url}/experiments/{xid}\n")


if __name__ == "__main__":
    main()
