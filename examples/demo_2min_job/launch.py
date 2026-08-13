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

"""Launch script for a 2-minute demo workload in XMC.

Demonstrates submitting an experiment and WorkUnits targeted at a specific ClusterQueue
to showcase real-time Resource Observability Dashboard (ROD) metrics.

Usage:
    python3 launch.py --queue=tpu-cluster-queue --workunits=3 --duration=120 --gpus=0 --cpu_milli=1000
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
    priority_pb2,
    resources_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

FLAGS = flags.FLAGS
flags.DEFINE_string("queue", "tpu-cluster-queue", "Target ClusterQueue name for workload")
flags.DEFINE_integer("duration", 120, "Workload duration in seconds (default: 120 seconds / 2 minutes)")
flags.DEFINE_integer("workunits", 3, "Number of WorkUnits to launch in this experiment")
flags.DEFINE_string("priority", "HIGH", "Priority string: HIGH, NORMAL, or LOW")
flags.DEFINE_integer("gpus", 0, "Number of GPUs requested (set to 0 for CPU-only)")
flags.DEFINE_integer("cpu_milli", 1000, "CPU millicores requested (default: 1000m / 1 CPU core)")
flags.DEFINE_integer("memory_mb", 512, "Memory in MB requested (default: 512MB)")
flags.DEFINE_string("author", "test@example.com", "Author / user email for demo workload")

# Skip git push snapshot during local test launch so SSH passphrase is not prompted
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def parse_priority(priority_str: str) -> priority_pb2.Priority.ValueType:
    """Parses priority string into protobuf Priority enum."""
    p_upper = priority_str.upper()
    if p_upper == "HIGH":
        return priority_pb2.Priority.HIGH
    if p_upper == "LOW":
        return priority_pb2.Priority.LOW
    return priority_pb2.Priority.NORMAL


def main(argv: Sequence[str]) -> None:
    """Launches 2-minute demonstration experiment for ROD live testing."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    os.environ["USER"] = FLAGS.author
    os.environ["LOGNAME"] = FLAGS.author

    image_path = os.environ.get("IMAGE_PATH", "python:3.11-slim")

    priority_enum = parse_priority(FLAGS.priority)
    duration_sec = FLAGS.duration
    queue_name = FLAGS.queue
    num_workunits = FLAGS.workunits

    print("=================================================================")
    print("       XMC 2-Minute Demo Workload Launching                      ")
    print("=================================================================")
    print(f"Target ClusterQueue : {queue_name}")
    print(f"Number of WorkUnits: {num_workunits}")
    print(f"Workload Duration  : {duration_sec} seconds")
    print(f"Priority           : {FLAGS.priority}")
    print(f"Requested Resources: {FLAGS.gpus} GPU(s), {FLAGS.cpu_milli}m CPU, {FLAGS.memory_mb}MB Memory per WorkUnit")
    print(f"Container Image    : {image_path}")
    print("-----------------------------------------------------------------")

    with xm_cloud.create_experiment(
        experiment_title=f"ROD Demo Workload ({num_workunits} WUs, {duration_sec}s execution)",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = experiment.resource_name.split("/")[-1]

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

        print(f"\n🚀 Creating {num_workunits} WorkUnit(s) under Experiment XID {xid}...")
        for i in range(num_workunits):
            cmd = [
                "python3",
                "-c",
                (
                    "import time, sys; "
                    f"print('Demo workload WU #{i+1} started for XID {xid}. Running for {duration_sec}s...'); "
                    "sys.stdout.flush(); "
                    f"time.sleep({duration_sec}); "
                    "print('Demo workload completed successfully!'); "
                    "sys.stdout.flush()"
                ),
            ]

            work_unit = work_unit_pb2.WorkUnit(
                work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
                priority=priority_enum,
                notes=f"ROD Demo WU #{i+1} (2-minute workload targeted at {queue_name})",
                tags=["rod-demo", "2min-workload", queue_name],
                executable=work_unit_pb2.Executable(
                    scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
                    jobset=work_unit_pb2.JobSetExecutable(
                        jobs=[
                            work_unit_pb2.KubernetesJob(
                                name=f"demo-worker-{i+1}",
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
            wu_id = created_wu.name.split("/")[-1]
            print(f"   ✅ WorkUnit #{i+1} created - ID: {wu_id}")

        print("\n📊 Monitor in Resource Observability Dashboard (ROD):")
        domain = os.environ.get("XMC_DOMAIN", "janzak.dev.xmanageriso.dev")
        print(f"   https://{domain}/resources?queue={queue_name}")
        print(f"   https://{domain}/experiments/{xid}")
        print("=================================================================\n")


if __name__ == "__main__":
    app.run(main)
