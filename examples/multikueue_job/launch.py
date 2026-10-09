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
"""Standalone MultiKueue Workload Launch Script for XMC."""

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

_QUEUE = flags.DEFINE_string("queue", "multikueue-cpu-queue", "Target MultiKueue LocalQueue.")
_NUM_WORK_UNITS = flags.DEFINE_integer("num_work_units", 2, "Number of WorkUnits to launch.")
_DURATION = flags.DEFINE_integer("duration", 20, "Execution duration in seconds per WorkUnit.")
snapshot_code.push_repo_state = lambda *a, **kw: type("Dummy", (), {"commit_url": None})()


def main(argv: Sequence[str]) -> None:
    """Launches the MultiKueue workload experiment."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
    if len(argv) > 1:
        raise app.UsageError(f"Unexpected positional arguments: {argv[1:]}")
    os.environ.setdefault("XMC_USER_EMAIL", "multikueue-user@google.com")
    n, dur, queue = _NUM_WORK_UNITS.value, _DURATION.value, _QUEUE.value
    acls = xid_messages_pb2.XidAcls(
        owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
    )
    res = resources_pb2.ResourceSet(
        cpu_resources=resources_pb2.CpuResources(cpu_millicores=250, memory_bytes=256 * 1024 * 1024)
    )
    title = f"MultiKueue Workload ({n} units, {dur}s execution)"
    with xm_cloud.create_experiment(
        experiment_title=title, priority=priority_pb2.Priority.NORMAL, acls=acls
    ) as exp:
        stub = exp._experiment_state_stub  # pylint: disable=protected-access
        xid = exp.resource_name.split("/")[-1]
        print(f"Created Experiment: {exp.resource_name} (XID: {xid})\nExperiment XID: {xid}")
        for i in range(1, n + 1):
            worker = f"multikueue-worker-{i}"
            cmd = f"import socket, time; print(socket.gethostname(), flush=True); time.sleep({dur})"
            spec = work_unit_pb2.ExecutableSpec(
                name="primary-worker",
                docker_image="python:3.11-slim",
                command=["python3", "-c", cmd],
                resources=res,
            )
            job = work_unit_pb2.KubernetesJob(name=worker, replicas=1, spec=spec)
            exec_pb = work_unit_pb2.Executable(
                scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue),
                jobset=work_unit_pb2.JobSetExecutable(jobs=[job]),
            )
            wu = work_unit_pb2.WorkUnit(
                work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
                priority=priority_pb2.Priority.NORMAL,
                notes=f"MultiKueue WorkUnit {i}/{n} on queue '{queue}'",
                tags=[queue, worker, "multikueue"],
                executable=exec_pb,
            )
            req = ess_api_pb2.CreateWorkUnitRequest(parent=exp.resource_name, work_unit=wu)
            print(f"  [{i}/{n}] Created: {stub.create_work_unit(req).name}")


if __name__ == "__main__":
    app.run(main)
