#!/usr/bin/env python3
"""Launch script demonstrating an XMC WorkUnit with a Chart Artifact for testing."""

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.dashboard_service.proto import (
    messages_pb2 as dashboard_messages_pb2,
)
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    artifact_pb2,
    priority_pb2,
    resources_pb2,
    url_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

# Skip git push snapshot during local test launch
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()


def main(argv: Sequence[str]) -> None:
    """Launches chart artifact validation experiment."""
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
        image_path = f"us-docker.pkg.dev/{project_id}/xmc/chart-artifact-job:latest"
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

    print(
        "Creating validation experiment with a Chart artifact on a WorkUnit"
        f" using image: {image_path}"
    )

    with xm_cloud.create_experiment(
        experiment_title="Chart Artifact Validation Experiment",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access

        # Extract XID from resource_name (e.g. 'experiments/12345')
        xid = int(experiment.resource_name.split("/")[-1])

        # Create a WorkUnit to attach the artifact to
        work_unit = work_unit_pb2.WorkUnit(
            work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
            priority=priority_pb2.Priority.NORMAL,
            notes="Work unit for testing ChartArtifact",
            tags=["chart-test"],
            executable=work_unit_pb2.Executable(
                scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
                jobset=work_unit_pb2.JobSetExecutable(
                    jobs=[
                        work_unit_pb2.KubernetesJob(
                            name="chart-worker",
                            replicas=1,
                            spec=work_unit_pb2.ExecutableSpec(
                                name="chart-worker",
                                docker_image=image_path,
                                command=["python3", "/app/worker.py"],
                                resources=resources_pb2.ResourceSet(
                                    cpu_resources=resources_pb2.CpuResources(
                                        cpu_millicores=100,
                                        memory_bytes=100 * 1024 * 1024,
                                    ),
                                ),
                            ),
                        ),
                    ]
                ),
            ),
        )

        create_wu_req = ess_api_pb2.CreateWorkUnitRequest(
            parent=experiment.resource_name,
            work_unit=work_unit,
        )
        created_wu = stub.create_work_unit(create_wu_req)
        print(f"Created Work Unit successfully: {created_wu.name}")

        # created_wu is an Operation with name
        # experiments/{xid}/workUnits/{wid}/operations/{op_id}
        # Extract WID from created_wu.name
        name_parts = created_wu.name.split("/")
        wid = int(name_parts[3])
        wu_name = f"experiments/{xid}/workUnits/{wid}"

        # Create the Artifact payload filling OUTPUT_ONLY fields.
        # NOTE: This is purely for testing the Artifact UI, and setting
        # OUTPUT_ONLY fields will be removed in a future PR.
        chart_payload = artifact_pb2.ChartArtifact(
            title="Test Chart Dashboard",
            metric_names=["accuracy", "loss"],
            # Filling OUTPUT_ONLY status field
            status=artifact_pb2.ChartArtifact.Status.SUCCESS,
            # Filling OUTPUT_ONLY chart field
            chart=dashboard_messages_pb2.Chart(
                name=f"{wu_name}/charts/test_chart",
                title="Accuracy and Loss Chart",
                description="A test chart for UI validation",
                xid=xid,
                wid=wid,
                plots=[
                    dashboard_messages_pb2.PlotSpec(
                        plot_id="plot_1",
                        title="Accuracy Plot",
                        type=dashboard_messages_pb2.PlotType.SCATTER,
                        data=dashboard_messages_pb2.DataSpec(
                            metrics=["accuracy"],
                        ),
                    )
                ],
                plot_layout=dashboard_messages_pb2.LayoutSpec(
                    type=dashboard_messages_pb2.LayoutSpec.LayoutType.GRID,
                    columns=1,
                ),
            ),
        )

        artifact = artifact_pb2.Artifact(
            tags=["chart-test", "validation"],
            notes="A mock chart artifact for testing the UI",
            lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.INPUT,
            url=url_pb2.Url(url="http://dummy-url"),
            payload=artifact_pb2.ArtifactPayload(chart=chart_payload),
        )

        create_req = ess_api_pb2.CreateArtifactRequest(
            parent=wu_name,
            artifact=artifact,
        )
        created_artifact = stub.create_artifact(create_req)

        print(f"Created Chart Artifact successfully: {created_artifact.name}")
        print(f"Experiment Dashboard URL: https://{os.environ.get('XMC_DOMAIN')}/experiments/{xid}")


if __name__ == "__main__":
    app.run(main)
