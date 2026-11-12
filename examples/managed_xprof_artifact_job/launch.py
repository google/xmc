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

# Bypass remote git repository check for standalone launch execution
snapshot_code.push_repo_state = lambda *_args, **_kwargs: type("Dummy", (), {"commit_url": None})()


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


def main() -> None:
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    project_id = os.environ.get("PROJECT_ID")
    default_image = os.environ.get("IMAGE_PATH")
    if not default_image and project_id:
        default_image = f"us-docker.pkg.dev/{project_id}/xmc/programmatic-xprof-job:latest"

    parser = argparse.ArgumentParser(description="Launch XMC + Diagon Profiling Experiment")
    parser.add_argument(
        "--experiment_name",
        "--experiment_title",
        default="ManagedXprof WorkUnit Artifact Example",
        help="Title of the experiment in XMC UI",
    )
    parser.add_argument(
        "--display_name",
        default="",
        help="UI display name for ManagedXprof artifact (defaults to <experiment_title>_<work_unit_id>)",
    )
    parser.add_argument(
        "--run_group",
        default="",
        help="Run group (tag) for the ManagedXprof profiling session (defaults to <experiment_title>)",
    )
    parser.add_argument(
        "--num_work_units",
        type=int,
        default=1,
        help="Number of work units to launch (default: 1)",
    )
    parser.add_argument(
        "--image_path",
        default=default_image,
        help="Container image to run in the cluster",
    )
    parser.add_argument(
        "--queue_name",
        default=os.environ.get("QUEUE_NAME", "cpu-queue"),
        help="Scheduling queue (default: cpu-queue)",
    )
    parser.add_argument(
        "--programmatic_steps",
        type=int,
        default=10,
        help="Initial steps traced programmatically (default: 10)",
    )
    parser.add_argument(
        "--total_steps",
        type=int,
        default=10000,
        help="Total steps to run (default: 10000)",
    )
    parser.add_argument(
        "--matrix_size",
        type=int,
        default=2000,
        help="Size of square matrices for profiling (default: 2000)",
    )
    args = parser.parse_args()

    if not args.image_path:
        parser.error(
            "PROJECT_ID environment variable must be set when --image_path is not specified."
        )

    domain = (os.environ.get("XMC_DOMAIN", "").strip() or "example.com").rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    ui_base_url = os.environ.get("XMANAGER_UI_URL", "").strip() or domain
    if not ui_base_url.startswith(("http://", "https://")):
        ui_base_url = f"https://{ui_base_url}"
    ui_base_url = ui_base_url.rstrip("/")
    os.environ["XMANAGER_UI_URL"] = ui_base_url

    print(f"🚀 Launching experiment '{args.experiment_name}'")
    print(f"   Image: {args.image_path}")
    print(f"   Queue: {args.queue_name} (Work Units: {args.num_work_units})")

    with xm_cloud.create_experiment(
        experiment_title=args.experiment_name,
        # No `acls`: XMC makes you (the creator) the owner. See docs/authentication.md.
    ) as experiment:
        stub = experiment._experiment_state_stub
        xid = int(experiment.resource_name.split("/")[-1])

        for _ in range(args.num_work_units):
            # 1. Create Work Unit
            work_unit = _build_work_unit(
                args.image_path,
                args.queue_name,
                programmatic_steps=args.programmatic_steps,
                total_steps=args.total_steps,
                matrix_size=args.matrix_size,
            )
            created_wu = stub.create_work_unit(
                ess_api_pb2.CreateWorkUnitRequest(
                    parent=experiment.resource_name,
                    work_unit=work_unit,
                )
            )
            parts = created_wu.name.split("/")
            wid = parts[3] if len(parts) >= 4 and parts[2] == "workUnits" else parts[-1]
            wu_name = f"experiments/{xid}/workUnits/{wid}"
            print(f"✅ Created Work Unit: {wu_name}")

            display_name = args.display_name or f"{args.experiment_name}_{wid}"
            run_group = args.run_group or args.experiment_name

            # 2. Attach ManagedXprof artifact immediately after work unit creation.
            # This is needed to make sure the reconciler is able to find the xprof artifact
            # during MLRun creation (EXECUTION phase triggers MLRun creation).
            # Note: A future improvement is synchronizing artifact and workunit creation,
            # which is not prioritized for now.
            artifact = artifact_pb2.Artifact(
                tags=["diagon-e2e", "managed_xprof"],
                notes="Work-unit-level ManagedXprof artifact for MLRun creation",
                lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
                payload=artifact_pb2.ArtifactPayload(
                    managed_xprof=artifact_pb2.ManagedXprofArtifact(
                        display_name=display_name,
                        run_group=run_group,
                    )
                ),
            )
            created_artifact = stub.create_artifact(
                ess_api_pb2.CreateArtifactRequest(
                    parent=wu_name,
                    artifact=artifact,
                )
            )
            print(f"📎 Attached ManagedXprof artifact: {created_artifact.name}")

        dashboard_url = f"{ui_base_url}/experiments/{xid}"
        print(f"\n🎉 Success! View Experiment Dashboard:\n👉 {dashboard_url}\n")


if __name__ == "__main__":
    main()
