# Copyright 2025 Google LLC
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
"""Utility functions for regression tests."""

from google.internal_lib.concurrent import parallel
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.experiment_state_server.proto import (
    artifact_pb2,
    priority_pb2,
    resources_pb2,
    status_pb2,
    url_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2


def _create_kubernetes_job(name: str) -> work_unit_pb2.KubernetesJob:
    """Creates a KubernetesJob proto with default resource allocations."""
    return work_unit_pb2.KubernetesJob(
        name=name,
        spec=work_unit_pb2.ExecutableSpec(
            resources=resources_pb2.ResourceSet(
                cpu_resources=resources_pb2.CpuResources(
                    cpu_millicores=100,
                    memory_bytes=1024 * 1024 * 100,
                )
            ),
            docker_image=("us-docker.pkg.dev/xmc-test-project-1/xmc/example-container"),
            env_vars={
                "EXIT_CODE": "0",
                "SLEEP_SECONDS": "60",
            },
        ),
        replicas=1,
        retry_limit=3,
    )


def get_work_unit() -> work_unit_pb2.WorkUnit:
    """Constructs a standard WorkUnit proto with client and server jobs."""
    return work_unit_pb2.WorkUnit(
        priority=priority_pb2.Priority.HIGH,
        work_unit_type=work_unit_pb2.WorkUnit.STANDARD,
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(
                queue_name="cpu-queue"
            ),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    _create_kubernetes_job(name="client"),
                    _create_kubernetes_job(name="server"),
                ]
            ),
        ),
    )


def create_experiment_with_work_units(
    examples: examples_lib.ExamplesLib,
    *,
    title: str = "Regression test",
    notes: str = "Regression test",
    work_unit_count: int = 2,
) -> str:
    """Creates an experiment with given number of work units.

    Args:
      examples: An ExamplesLib instance.
      title: The title of the experiment.
      notes: Notes for the experiment.
      work_unit_count: The number of work units to create.

    Returns:
      The name of the created experiment.
    """
    acls = xid_messages_pb2.XidAcls(
        owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
    )
    experiment = examples.create_experiment(
        title=title,
        tags=["regression", "test"],
        acls=acls,
        urls=[],
        notes=notes,
    )
    experiment_name = experiment.name

    work_unit = get_work_unit()
    kwargs_list = (
        {
            "experiment_name": experiment_name,
            "work_unit_id": i,
            "work_unit": work_unit,
        }
        for i in range(1, work_unit_count + 1)
    )
    parallel.RunInParallel(
        function=examples.create_work_unit,
        list_of_kwargs_to_function=kwargs_list,
        num_workers=work_unit_count,
    )

    examples.set_experiment_launch_state(
        experiment_name, status_pb2.ExperimentStatus.LaunchState.COMPLETED
    )
    return experiment_name


def prepare_artifact(
    *,
    data_type: artifact_pb2.Artifact.DataType,
    notes: str,
    lifecycle_phase: artifact_pb2.Artifact.LifecyclePhase,
    url: str,
    display_name: str,
) -> artifact_pb2.Artifact:
    """Creates an artifact proto."""
    return artifact_pb2.Artifact(
        data_type=data_type,
        notes=notes,
        lifecycle_phase=lifecycle_phase,
        url=url_pb2.Url(
            url=url,
            display_name=display_name,
        ),
    )


def create_experiment_with_artifacts(
    examples: examples_lib.ExamplesLib,
    *,
    title: str = "Artifact Regression Test",
    notes: str = "Test with artifacts",
    work_unit_count: int = 1,
    experiment_artifacts_count: int = 1,
    work_unit_artifacts_count: int = 1,
) -> tuple[str, list[str], list[str]]:
    """Creates an experiment with work units and associated artifacts.

    Args:
      examples: An ExamplesLib instance.
      title: The title of the experiment.
      notes: Notes for the experiment.
      work_unit_count: The number of work units to create.
      experiment_artifacts_count: Number of artifacts to add to the experiment.
      work_unit_artifacts_count: Number of artifacts to add to EACH work unit.

    Returns:
      A tuple (experiment_name, experiment_artifact_names,
      work_unit_artifact_names), where experiment_name is the name of the
      created experiment, experiment_artifact_names is the list of experiment
      artifact names, and work_unit_artifact_names is the list of work unit
      artifact names.
    """
    experiment_name = create_experiment_with_work_units(
        examples,
        title=title,
        notes=notes,
        work_unit_count=work_unit_count,
    )

    # Create Experiment-level artifacts
    experiment_artifact_names = []
    for i in range(experiment_artifacts_count):
        artifact = prepare_artifact(
            data_type=artifact_pb2.Artifact.TEXT,
            notes=f"Experiment artifact {i}",
            lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
            url=f"http://example.com/exp/{i}",
            display_name=f"Exp Artifact {i}",
        )
        result = examples.create_artifact(
            parent_name=experiment_name, artifact=artifact
        )
        experiment_artifact_names.append(result.name)

    # Create Work Unit-level artifacts
    work_unit_artifact_names = []
    for wu_id in range(1, work_unit_count + 1):
        work_unit_name = f"{experiment_name}/workUnits/{wu_id}"
        for i in range(work_unit_artifacts_count):
            artifact = prepare_artifact(
                data_type=artifact_pb2.Artifact.URL,
                notes=f"Work Unit {wu_id} artifact {i}",
                lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
                url=f"http://example.com/wu/{wu_id}/art/{i}",
                display_name=f"WU {wu_id} Artifact {i}",
            )
            result = examples.create_artifact(
                parent_name=work_unit_name, artifact=artifact
            )
            work_unit_artifact_names.append(result.name)

    return (experiment_name, experiment_artifact_names, work_unit_artifact_names)
