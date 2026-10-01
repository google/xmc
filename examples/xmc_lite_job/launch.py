#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Launches an XMC Lite experiment on GKE using xm_local.Kubernetes."""

import json
import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app, flags
from xmanager import xm, xm_local
from xmanager.xm_local import executables as local_executables

from examples.xmc_lite_job import k8s_workload_client

logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

_EXP_NAME = flags.DEFINE_string("exp_name", "xmc-lite-job", "Title of the experiment in SQLite.")
_WORKLOAD_MODE = flags.DEFINE_enum(
    "workload_mode", "jobset", ["jobset", "job"], "Kubernetes workload kind."
)
_NAMESPACE = flags.DEFINE_string(
    "namespace", os.environ.get("K8S_NAMESPACE", "default"), "K8s namespace."
)
_LOCAL_QUEUE = flags.DEFINE_string("local_queue", "", "Optional Kueue LocalQueue name.")
_NUM_WORK_UNITS = flags.DEFINE_integer("num_work_units", 1, "Number of WorkUnits to launch.")
_DURATION = flags.DEFINE_integer("duration", 30, "Worker sleep duration in seconds.")
_IMAGE = flags.DEFINE_string("image", "python:3.11-slim", "Container image URI to execute on GKE.")
_DRY_RUN = flags.DEFINE_bool(
    "dry_run", False, "Record manifests and SQLite state without calling GKE."
)


def build_xm_job(work_unit_index: int, duration_seconds: int, image: str) -> xm.Job:
    """Constructs an xm.Job configured with the xm_local.Kubernetes executor.

    Args:
      work_unit_index: 1-based index of the WorkUnit.
      duration_seconds: Runtime duration in seconds.
      image: Container image path.

    Returns:
      An xm.Job ready to be added to a LocalExperiment.
    """
    script = (
        "import time; "
        f"print('Starting WorkUnit {work_unit_index} ({duration_seconds}s)...', flush=True); "
        f"time.sleep({duration_seconds}); "
        f"print('Completed WorkUnit {work_unit_index}.', flush=True)"
    )
    executable = local_executables.GoogleContainerRegistryImage(
        name="worker",
        image_path=image,
        args=xm.SequentialArgs.from_collection(["python3", "-c", script]),
    )
    executor = xm_local.Kubernetes(requirements=xm.JobRequirements(cpu=1, ram=1 * xm.GiB))
    return xm.Job(executable=executable, executor=executor, name="worker")


def run_experiment(
    exp_name: str,
    num_work_units: int,
    duration_seconds: int,
    image: str,
    client_config: k8s_workload_client.WorkloadClientConfig,
) -> tuple[int, list[dict[str, object]]]:
    """Creates an xm_local experiment, schedules WorkUnits, and returns manifests.

    Args:
      exp_name: Experiment title saved to SQLite.
      num_work_units: Number of WorkUnits to launch.
      duration_seconds: Worker sleep duration in seconds.
      image: Container image URI.
      client_config: WorkloadClientConfig for Kubernetes submission.

    Returns:
      A tuple of (experiment_id, recorded_k8s_manifests).
    """
    with k8s_workload_client.active_xmc_lite_client(client_config) as k8s_client:
        with xm_local.create_experiment(experiment_title=exp_name) as experiment:
            for idx in range(1, num_work_units + 1):
                experiment.add(build_xm_job(idx, duration_seconds, image))
            return experiment.experiment_id, list(k8s_client.recorded_manifests)


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for launching an XMC Lite experiment."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")
    config = k8s_workload_client.WorkloadClientConfig(
        namespace=_NAMESPACE.value,
        workload_mode=_WORKLOAD_MODE.value,
        local_queue=_LOCAL_QUEUE.value,
        dry_run=_DRY_RUN.value,
    )
    exp_id, manifests = run_experiment(
        _EXP_NAME.value,
        _NUM_WORK_UNITS.value,
        _DURATION.value,
        _IMAGE.value,
        config,
    )
    print(f"Created local experiment_id={exp_id} (mode={config.workload_mode})")
    if config.dry_run:
        print(json.dumps(manifests, indent=2))


if __name__ == "__main__":
    app.run(main)
