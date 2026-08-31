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

"""Launches an experiment that feeds ClickHouse and exposes the result as a Chart artifact.

The launcher performs four steps:

1. Creates a WorkUnit whose worker streams randomized metrics into ClickHouse.
2. Creates a Dashboard together with a Chart plotting those metric names, in one
   transaction, so the dashboard is never persisted referencing a chart that failed.
3. Creates a ChartArtifact on the WorkUnit listing those metric names.
4. Creates a URL artifact on the same WorkUnit pointing at the Dashboard.

The Artifacts tab renders a ChartArtifact as its metric list plus the server-provisioned
chart resource, not as plots, and it does not render that artifact's URL. The plots live
on the Dashboard, and the companion URL artifact is the one-click route to them.
"""

import logging
import os
from collections.abc import Sequence

import dotenv
from absl import app
from dashboard_rest_client import DashboardRestClient, resource_id_from_name
from google.protobuf import json_format
from iap_identity_token import mint_identity_token
from training_metrics import (
    ACCURACY_METRIC,
    LEARNING_RATE_METRIC,
    LOSS_METRIC,
    METRIC_NAMES,
)
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

_DEFAULT_GATEWAY_URL = "http://ingestion-gateway-service.xmc.svc.cluster.local:8080/api/v1/metrics"
_EXPERIMENT_TITLE = "Chart Artifact Experiment"


def _require_env(name: str) -> str:
    """Reads a required environment variable or fails with actionable usage guidance."""
    value = os.environ.get(name, "").strip()
    if not value:
        raise app.UsageError(f"{name} must be set in your environment or .env file.")
    return value


def _resolve_ui_base_url() -> str:
    """Resolves the XMC UI origin, which also fronts the Dashboard Service REST routes."""
    if ui_url := os.environ.get("XMANAGER_UI_URL", "").strip():
        return ui_url.rstrip("/")
    return f"https://{_require_env('XMC_DOMAIN')}"


def _resolve_image_path() -> str:
    """Resolves the worker image, defaulting to the tag pushed by build_and_push.sh."""
    if image_path := os.environ.get("IMAGE_PATH", "").strip():
        return image_path
    return f"us-docker.pkg.dev/{_require_env('PROJECT_ID')}/xmc/chart-artifact-job:latest"


def _scatter_plot(plot_id: str, title: str, metric_name: str) -> dashboard_messages_pb2.PlotSpec:
    """Builds a scatter plot of one metric against training steps."""
    return dashboard_messages_pb2.PlotSpec(
        plot_id=plot_id,
        title=title,
        type=dashboard_messages_pb2.PlotType.SCATTER,
        data=dashboard_messages_pb2.DataSpec(metrics=[metric_name]),
    )


def _summary_plot(plot_id: str, title: str, metric_name: str) -> dashboard_messages_pb2.PlotSpec:
    """Builds a summary box showing the latest value of one metric."""
    return dashboard_messages_pb2.PlotSpec(
        plot_id=plot_id,
        title=title,
        type=dashboard_messages_pb2.PlotType.SUMMARY_BOX,
        data=dashboard_messages_pb2.DataSpec(metrics=[metric_name]),
    )


def build_training_chart(xid: int, wid: int) -> dashboard_messages_pb2.Chart:
    """Builds the Chart plotting the metrics the worker writes to ClickHouse.

    Args:
        xid: Experiment ID owning the work unit.
        wid: Work unit ID whose metrics the plots read.

    Returns:
        An unsaved Chart message; the server assigns its name and ID on creation.
    """
    return dashboard_messages_pb2.Chart(
        title="Randomized Training Run",
        description="Metrics streamed to ClickHouse by the chart artifact worker.",
        xid=xid,
        wid=wid,
        plots=[
            _summary_plot("latest_loss", "Loss (latest)", LOSS_METRIC),
            _summary_plot("latest_accuracy", "Accuracy (latest)", ACCURACY_METRIC),
            _scatter_plot("loss_curve", "Loss vs Steps", LOSS_METRIC),
            _scatter_plot("accuracy_curve", "Accuracy vs Steps", ACCURACY_METRIC),
            _scatter_plot("lr_curve", "Learning Rate vs Steps", LEARNING_RATE_METRIC),
        ],
        plot_layout=dashboard_messages_pb2.LayoutSpec(
            type=dashboard_messages_pb2.LayoutSpec.LayoutType.GRID,
            columns=2,
        ),
    )


def build_work_unit(image_path: str, queue_name: str, xid: int) -> work_unit_pb2.WorkUnit:
    """Builds the single-job WorkUnit whose worker streams metrics into ClickHouse.

    The actuator injects `XMC_WORK_UNIT_ID` and `XMC_EXECUTION_ID`, but not `XMC_XID`,
    so the launcher supplies it alongside the ingestion gateway address.
    """
    gateway_url = os.environ.get("XMC_GATEWAY_URL", _DEFAULT_GATEWAY_URL)
    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="Streams randomized metrics to ClickHouse for a Chart artifact.",
        tags=["chart-artifact", "metrics"],
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
                                    cpu_millicores=500,
                                    memory_bytes=512 * 1024 * 1024,
                                ),
                            ),
                            env=[
                                work_unit_pb2.EnvVar(name="XMC_GATEWAY_URL", value=gateway_url),
                                work_unit_pb2.EnvVar(name="XMC_XID", value=str(xid)),
                            ],
                        ),
                    ),
                ]
            ),
        ),
    )


def provision_dashboard(client: DashboardRestClient, xid: int, wid: int) -> str:
    """Creates the Dashboard and, in the same transaction, the Chart it renders.

    Args:
        client: Authenticated Dashboard Service REST client.
        xid: Experiment ID owning the work unit.
        wid: Work unit ID the chart belongs to.

    Returns:
        The dashboard resource ID, for building the UI deep link.
    """
    dashboard = dashboard_messages_pb2.Dashboard(
        title=f"{_EXPERIMENT_TITLE} {xid}",
        description=f"Randomized training metrics for experiments/{xid}/workUnits/{wid}.",
        chart_layout=dashboard_messages_pb2.LayoutSpec(
            type=dashboard_messages_pb2.LayoutSpec.LayoutType.GRID,
            columns=1,
        ),
    )
    created = client.create_dashboard(
        dashboard=json_format.MessageToDict(dashboard),
        dashboard_id=f"chart_artifact_x{xid}_w{wid}",
        charts=[json_format.MessageToDict(build_training_chart(xid, wid))],
    )
    print(f"Created Dashboard successfully: {created['name']}")
    return resource_id_from_name(created["name"])


def create_chart_artifact(stub, work_unit_name: str, dashboard_url: str):
    """Attaches the ChartArtifact to the work unit.

    Only `title` and `metric_names` may be supplied: the server rejects client-set
    `chart`, `status` and `error_message`, marks the artifact PROVISIONING, and fills
    those fields once it has provisioned its own chart resource.

    The UI does not render `Artifact.url` for chart payloads, so the dashboard link is
    repeated in the notes and carried by the companion URL artifact.
    """
    artifact = artifact_pb2.Artifact(
        tags=["chart-artifact", "metrics"],
        notes=f"Randomized training metrics streamed to ClickHouse. Plots: {dashboard_url}",
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
        priority=1,
        url=url_pb2.Url(url=dashboard_url),
        payload=artifact_pb2.ArtifactPayload(
            chart=artifact_pb2.ChartArtifact(
                title="Randomized Training Run",
                metric_names=list(METRIC_NAMES),
            )
        ),
    )
    request = ess_api_pb2.CreateArtifactRequest(parent=work_unit_name, artifact=artifact)
    return stub.create_artifact(request)


def create_dashboard_link_artifact(stub, work_unit_name: str, dashboard_url: str):
    """Attaches a URL artifact linking the work unit to its plotted dashboard.

    A chart artifact renders as its metric list and provisioned chart resource — the
    Artifacts tab draws no plots and offers no link for it. This companion URL artifact
    is what makes the dashboard reachable in one click from the same tab.
    """
    artifact = artifact_pb2.Artifact(
        tags=["chart-artifact", "metrics"],
        notes="Plots of the metrics listed by the chart artifact above.",
        lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
        priority=2,
        url=url_pb2.Url(url=dashboard_url),
        payload=artifact_pb2.ArtifactPayload(
            url=artifact_pb2.UrlArtifact(
                url=dashboard_url,
                icon="bar_chart",
                display_name="Metric plots (dashboard)",
            )
        ),
    )
    request = ess_api_pb2.CreateArtifactRequest(parent=work_unit_name, artifact=artifact)
    return stub.create_artifact(request)


def main(argv: Sequence[str]) -> None:
    """Launches the experiment and provisions its chart, dashboard and artifact."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    image_path = _resolve_image_path()
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")
    ui_base_url = _resolve_ui_base_url()
    identity_token = mint_identity_token(
        service_account=_require_env("XMC_CLIENT_SA"),
        audience=_require_env("IAP_CLIENT_ID"),
    )

    print(f"Creating chart artifact experiment using image: {image_path}")

    with xm_cloud.create_experiment(
        experiment_title=_EXPERIMENT_TITLE,
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = int(experiment.resource_name.split("/")[-1])

        created_wu = stub.create_work_unit(
            ess_api_pb2.CreateWorkUnitRequest(
                parent=experiment.resource_name,
                work_unit=build_work_unit(image_path, queue_name, xid),
            )
        )
        print(f"Created Work Unit successfully: {created_wu.name}")

        # created_wu is an Operation named experiments/{xid}/workUnits/{wid}/operations/{op}.
        wid = int(created_wu.name.split("/")[3])
        work_unit_name = f"experiments/{xid}/workUnits/{wid}"

        client = DashboardRestClient(base_url=ui_base_url, identity_token=identity_token)
        dashboard_id = provision_dashboard(client, xid, wid)
        dashboard_url = f"{ui_base_url}/dashboards/{dashboard_id}"

        created_chart_artifact = create_chart_artifact(stub, work_unit_name, dashboard_url)
        print(f"Created Chart Artifact successfully: {created_chart_artifact.name}")

        created_link_artifact = create_dashboard_link_artifact(stub, work_unit_name, dashboard_url)
        print(f"Created Dashboard link artifact: {created_link_artifact.name}")

        print(f"\nWork Unit:  {ui_base_url}/{work_unit_name}")
        print(f"Dashboard:  {dashboard_url}")
        print("\nPlots appear once the worker has streamed its first metrics to ClickHouse.")


if __name__ == "__main__":
    app.run(main)
