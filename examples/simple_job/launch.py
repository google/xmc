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

"""Launch script for demonstration workload in XMC with multiple WorkUnits.

Demonstrates submitting an experiment and multiple WorkUnits targeted at a specific ClusterQueue
to showcase workload lifecycle.

Usage:
    python3 launch.py --queue=cpu-queue --num_work_units=3 --duration=60 --gpus=0
"""

from __future__ import annotations

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app, flags
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


FLAGS = flags.FLAGS
flags.DEFINE_string(
    "queue",
    os.environ.get("QUEUE_NAME", "cpu-queue"),
    "Target Queue name for workload (default: cpu-queue)",
)
flags.DEFINE_integer("num_work_units", 3, "Number of WorkUnits to create (default: 3)")
flags.DEFINE_integer(
    "duration", 60, "Workload duration in seconds (default: 60 seconds / 1 minute)"
)
flags.DEFINE_string("priority", "HIGH", "Priority string: HIGH, NORMAL, or LOW")
flags.DEFINE_integer("gpus", 0, "Number of GPUs requested (default: 0 for CPU-only)")
flags.DEFINE_integer("cpu_milli", 500, "CPU millicores requested (default: 500m)")
flags.DEFINE_integer("memory_mb", 512, "Memory in MB requested (default: 512MB)")
flags.DEFINE_string(
    "author",
    os.environ.get("USER", "test@example.com"),
    "Author / user email for demo workload",
)

# Skip git push snapshot during test launch so SSH passphrase is not prompted
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def parse_priority(priority_str: str) -> priority_pb2.Priority.ValueType:
    """Parses priority string into protobuf Priority enum."""
    p_upper = priority_str.upper()
    if p_upper == "HIGH":
        return priority_pb2.Priority.HIGH
    if p_upper == "LOW":
        return priority_pb2.Priority.LOW
    return priority_pb2.Priority.NORMAL


def _resolve_ui_base_url() -> str:
    """Resolves the XMC UI origin URL from environment variables and sets XMANAGER_UI_URL."""
    if ui_url := os.environ.get("XMANAGER_UI_URL", "").strip():
        return ui_url.rstrip("/")
    domain = (
        os.environ.get("XMC_DOMAIN", "").strip()
        or os.environ.get("DOMAIN_NAME", "").strip()
        or os.environ.get("GCP_DOMAIN_NAME", "").strip()
        or "dev.xmanageriso.dev"
    ).rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    os.environ["XMANAGER_UI_URL"] = domain
    return domain


def main(argv: Sequence[str]) -> None:
    """Launches demonstration experiment with multiple WorkUnits for live testing."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    os.environ["USER"] = FLAGS.author
    os.environ["LOGNAME"] = FLAGS.author

    project_id = os.environ.get("PROJECT_ID") or os.environ.get("GCP_PROJECT_ID") or "xmc-project"
    image_path = os.environ.get("IMAGE_PATH", "python:3.11-slim")

    priority_enum = parse_priority(FLAGS.priority)
    duration_sec = FLAGS.duration
    queue_name = FLAGS.queue
    num_work_units = max(1, FLAGS.num_work_units)
    ui_base_url = _resolve_ui_base_url()

    print("=================================================================")
    print("       XMC Multi-WorkUnit Demo Workload Launching                ")
    print("=================================================================")
    print(f"Project ID         : {project_id}")
    print(f"Target ClusterQueue: {queue_name}")
    print(f"Number of WorkUnits: {num_work_units}")
    print(f"Workload Duration  : {duration_sec} seconds per unit")
    print(f"Priority           : {FLAGS.priority}")
    print(
        f"Requested Resources: {FLAGS.gpus} GPU(s), {FLAGS.cpu_milli}m CPU, "
        f"{FLAGS.memory_mb}MB Memory"
    )
    print(f"Container Image    : {image_path}")
    print(f"UI Base URL        : {ui_base_url}")
    print("-----------------------------------------------------------------")

    with xm_cloud.create_experiment(
        experiment_title=f"Demo Workload ({num_work_units} units, {duration_sec}s execution)",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = experiment.resource_name.split("/")[-1]

        print(f"\n🚀 Experiment created successfully: {experiment.resource_name} (XID: {xid})")
        print("Submitting WorkUnits...")

        # Prepare resource requests
        res_kwargs = {
            "cpu_resources": resources_pb2.CpuResources(
                cpu_millicores=FLAGS.cpu_milli,
                memory_bytes=FLAGS.memory_mb * 1024 * 1024,
            )
        }
        if FLAGS.gpus > 0:
            res_kwargs["gpu_resources"] = resources_pb2.GpuResources(
                count=FLAGS.gpus,
                type="nvidia.com/gpu",
            )

        created_work_units: list[str] = []

        for i in range(1, num_work_units + 1):
            wu_worker_name = f"demo-worker-{i}" if num_work_units > 1 else "demo-worker"
            cmd = [
                "python3",
                "-c",
                (
                    "import time, sys; "
                    f"print('Demo workload ({wu_worker_name}) started for XID {xid}. "
                    f"Running for {duration_sec}s...'); "
                    "sys.stdout.flush(); "
                    f"time.sleep({duration_sec}); "
                    f"print('Demo workload ({wu_worker_name}) completed successfully!'); "
                    "sys.stdout.flush()"
                ),
            ]

            work_unit = work_unit_pb2.WorkUnit(
                work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
                priority=priority_enum,
                notes=(
                    f"Demo workload {i}/{num_work_units} ({wu_worker_name}) "
                    f"targeted at {queue_name}"
                ),
                tags=[f"{duration_sec}s-workload", queue_name, wu_worker_name],
                executable=work_unit_pb2.Executable(
                    scheduling_constraints=work_unit_pb2.SchedulingConstraints(
                        queue_name=queue_name
                    ),
                    jobset=work_unit_pb2.JobSetExecutable(
                        jobs=[
                            work_unit_pb2.KubernetesJob(
                                name=wu_worker_name,
                                replicas=1,
                                spec=work_unit_pb2.ExecutableSpec(
                                    docker_image=image_path,
                                    command=cmd,
                                    resources=resources_pb2.ResourceSet(**res_kwargs),
                                ),
                            )
                        ]
                    ),
                ),
            )

            create_req = ess_api_pb2.CreateWorkUnitRequest(
                parent=experiment.resource_name,
                work_unit=work_unit,
            )
            created_wu = stub.create_work_unit(create_req)
            # created_wu.name may be formatted as experiments/{xid}/workUnits/{wid}
            # or experiments/{xid}/workUnits/{wid}/operations/{op}
            wu_parts = created_wu.name.split("/")
            wu_id = (
                wu_parts[3] if len(wu_parts) > 3 and wu_parts[2] == "workUnits" else wu_parts[-1]
            )
            created_work_units.append(wu_id)
            print(f"  [+] WorkUnit #{i} created: ID={wu_id} (Worker={wu_worker_name})")

        print("\n✅ All WorkUnits created successfully!")
        print(f"   Experiment XID: {xid}")
        print(f"   WorkUnit IDs  : {', '.join(created_work_units)}")
        print("\n📊 Monitor in XMC Web UI:")
        print(f"   {ui_base_url}/experiments/{xid}")
        print("=================================================================\n")


if __name__ == "__main__":
    app.run(main)
