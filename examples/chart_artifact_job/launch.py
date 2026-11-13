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

from __future__ import annotations

import json
import logging
import os
import random
import sys
import urllib.error
import urllib.request
from collections.abc import Sequence
from typing import TYPE_CHECKING

import dotenv
from absl import app
from google.protobuf import json_format
from training_metrics import (
    ACCURACY_METRIC,
    LEARNING_RATE_METRIC,
    LOSS_METRIC,
    METRIC_NAMES,
)
from xdash import (
    ChartBuilder,
    DashboardBuilder,
    DashboardClient,
    resolve_auth_headers,
    scatter_plot,
    summary_kpi_plot,
)
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

if TYPE_CHECKING:
    # Type-only imports resolved via xdash._protos and xmanager_cloud;
    # `from __future__ import annotations` keeps them out of the runtime path.
    from xdash._protos import messages_pb2 as dashboard_messages_pb2
    from xmanager_cloud.experiment_state_server import experiment_state_api

# Skip git push snapshot during local test launch
snapshot_code.push_repo_state = lambda *_args, **_kwargs: type("Dummy", (), {"commit_url": None})()

_DEFAULT_GATEWAY_URL = "http://ingestion-gateway-service.xmc.svc.cluster.local:8080/api/v1/metrics"
_DEFAULT_TOTAL_STEPS = "60"
_EXPERIMENT_TITLE = "Chart Artifact Experiment"
_CHART_TITLE = "Randomized Training Run"


def _require_env(name: str) -> str:
    """Reads a required environment variable or fails with actionable usage guidance."""
    value = os.environ.get(name, "").strip()
    if not value:
        raise app.UsageError(f"{name} must be set in your environment or .env file.")
    return value


def _get_env_bool(key: str, default: bool = True) -> bool:
    """Parses a boolean value from environment variables."""
    val = os.environ.get(key, "").strip().lower()
    if not val:
        return default
    return val in {"1", "true", "yes", "t", "y"}


def _resolve_dashboard_endpoint() -> str:
    """Resolves the dashboard service endpoint URL from environment variables.

    Derives the endpoint from XMC_DASHBOARD_ENDPOINT, XMANAGER_UI_URL, or
    XMANAGER_ENDPOINT (stripping gRPC/DNS prefixes and appending /v1/dashboards).
    """
    if custom_endpoint := os.environ.get("XMC_DASHBOARD_ENDPOINT") or os.environ.get(
        "DASHBOARD_ENDPOINT"
    ):
        return custom_endpoint

    if ui_url := os.environ.get("XMANAGER_UI_URL"):
        return f"{ui_url.rstrip('/')}/v1/dashboards"

    xmanager_endpoint = os.environ.get("XMANAGER_ENDPOINT", "")
    if not xmanager_endpoint:
        return "http://localhost:8080/v1/dashboards"

    endpoint = xmanager_endpoint
    for prefix in ("dns:///", "dns://", "grpc://", "https://", "http://"):
        if endpoint.startswith(prefix):
            endpoint = endpoint[len(prefix) :]
    endpoint = endpoint.split(":")[0]  # Remove port if present

    if endpoint.startswith("grpc.api."):
        endpoint = endpoint[len("grpc.api.") :]
    elif endpoint.startswith("grpc."):
        endpoint = endpoint[len("grpc.") :]

    scheme = "http" if endpoint in {"localhost", "127.0.0.1", "::1", "0.0.0.0"} else "https"
    return f"{scheme}://{endpoint}/v1/dashboards"


def _resolve_ui_base_url() -> str:
    """Resolves the XMC UI origin, used to build the deep links printed at the end.

    XMC_DOMAIN is documented as a bare host, but the same value is often pasted in
    with the scheme it was copied from a browser with. Prefixing that blindly yields
    https://https://example.com, so an existing scheme is kept and only a missing one
    is added.
    """
    if ui_url := os.environ.get("XMANAGER_UI_URL", "").strip():
        return ui_url.rstrip("/")
    domain = _require_env("XMC_DOMAIN").strip().rstrip("/")
    if not domain.startswith(("http://", "https://")):
        domain = f"https://{domain}"
    os.environ["XMANAGER_UI_URL"] = domain
    return domain


def _resolve_image_path() -> str:
    """Resolves the worker image, defaulting to the tag pushed by build_and_push.sh."""
    if image_path := os.environ.get("IMAGE_PATH", "").strip():
        return image_path
    return f"us-docker.pkg.dev/{_require_env('PROJECT_ID')}/xmc/chart-artifact-job:latest"


def build_training_chart(xid: int, wid: int) -> dashboard_messages_pb2.Chart:
    """Builds the Chart plotting the metrics the worker writes to ClickHouse.

    The chart is created inline with the dashboard, so the server renames it, generates
    its ID and appends that ID to dashboard.chart_ids. Hence no `set_name()` here, and no
    `DashboardBuilder.add_chart()` on the dashboard side: either would declare a
    reference to a chart that is supposed to already exist, which the server rejects.

    Args:
        xid: Experiment ID owning the work unit.
        wid: Work unit ID whose metrics the plots read.

    Returns:
        An unsaved Chart message.
    """
    return (
        ChartBuilder()
        .for_experiment(xid=xid, wid=wid)
        .set_title(_CHART_TITLE)
        .set_description("Metrics streamed to ClickHouse by the chart artifact worker.")
        .set_grid_layout(columns=2)
        .add_plot(summary_kpi_plot("latest_loss", "Loss (latest)", [LOSS_METRIC]))
        .add_plot(summary_kpi_plot("latest_accuracy", "Accuracy (latest)", [ACCURACY_METRIC]))
        .add_plot(scatter_plot("loss_curve", "Loss vs Steps", [LOSS_METRIC]))
        .add_plot(scatter_plot("accuracy_curve", "Accuracy vs Steps", [ACCURACY_METRIC]))
        .add_plot(scatter_plot("lr_curve", "Learning Rate vs Steps", [LEARNING_RATE_METRIC]))
        .build()
    )


def _resolve_seed() -> str:
    """Resolves the sampler seed, drawing a random one when none is configured.

    The seed is drawn by the launcher rather than the worker so that its value is known
    up front and can be printed: a run is only reproducible if its seed is visible
    without digging through pod logs.
    """
    return os.environ.get("CHART_DEMO_SEED", "").strip() or str(random.randrange(1, 1_000_000))


def build_worker_env(xid: int, seed: str) -> list[work_unit_pb2.EnvVar]:
    """Resolves the environment the worker container runs with.

    The pod never sees `worker.env` or `.env`, so every knob the worker reads has to be
    forwarded explicitly. The actuator injects `XMC_WORK_UNIT_ID` and `XMC_EXECUTION_ID`,
    but not `XMC_XID`, so the launcher supplies it alongside the gateway address.

    Args:
        xid: Experiment ID the worker tags its metrics with.
        seed: Sampler seed, as resolved by `_resolve_seed`.

    Returns:
        The environment variables to pass to the worker container.
    """
    return [
        work_unit_pb2.EnvVar(
            name="XMC_GATEWAY_URL",
            value=os.environ.get("XMC_GATEWAY_URL", _DEFAULT_GATEWAY_URL),
        ),
        work_unit_pb2.EnvVar(name="XMC_XID", value=str(xid)),
        work_unit_pb2.EnvVar(
            name="CHART_DEMO_TOTAL_STEPS",
            value=os.environ.get("CHART_DEMO_TOTAL_STEPS", _DEFAULT_TOTAL_STEPS),
        ),
        work_unit_pb2.EnvVar(name="CHART_DEMO_SEED", value=seed),
    ]


def build_work_unit(
    image_path: str, queue_name: str, worker_env: Sequence[work_unit_pb2.EnvVar]
) -> work_unit_pb2.WorkUnit:
    """Builds the single-job WorkUnit whose worker streams metrics into ClickHouse.

    Args:
        image_path: Container image holding worker.py.
        queue_name: Target Kueue queue.
        worker_env: Environment variables passed to the worker container.

    Returns:
        The WorkUnit message to create.
    """
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
                            command=["python3", "-u", "/app/worker.py"],
                            resources=resources_pb2.ResourceSet(
                                cpu_resources=resources_pb2.CpuResources(
                                    cpu_millicores=500,
                                    memory_bytes=512 * 1024 * 1024,
                                ),
                            ),
                            env=worker_env,
                        ),
                    ),
                ]
            ),
        ),
    )


def dashboard_id_for(xid: int, wid: int) -> str:
    """Returns the deterministic dashboard resource ID for a work unit.

    Derived rather than read back from the server so the artifacts can link to the
    dashboard even when its creation was skipped or failed.
    """
    return f"chart_artifact_x{xid}_w{wid}"


def _publish_dashboard_over_rest(
    dashboard_endpoint: str,
    dashboard: dashboard_messages_pb2.Dashboard,
    dashboard_id: str,
    chart: dashboard_messages_pb2.Chart,
) -> None:
    """Creates the Dashboard and its Chart through the transcoded REST route."""
    print(f"--> Publishing Dashboard '{dashboard.title}' to HTTP REST {dashboard_endpoint}...")
    payload = {
        "dashboard": json_format.MessageToDict(dashboard, preserving_proto_field_name=False),
        "dashboard_id": dashboard_id,
        "charts": [json_format.MessageToDict(chart, preserving_proto_field_name=False)],
    }
    req = urllib.request.Request(
        dashboard_endpoint,
        data=json.dumps(payload).encode("utf-8"),
        headers=resolve_auth_headers(dashboard_endpoint),
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            created = json.loads(resp.read().decode("utf-8"))
            print(f"--> Created Dashboard successfully: {dashboard.name} (HTTP {resp.status})")
            print(f"--> Server-assigned chart IDs: {created.get('chartIds', [])}")
    except urllib.error.HTTPError as http_err:
        err_msg = http_err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {http_err.code} {http_err.reason}: {err_msg}") from http_err


def _publish_dashboard_over_grpc(
    dashboard_endpoint: str,
    dashboard: dashboard_messages_pb2.Dashboard,
    dashboard_id: str,
    chart: dashboard_messages_pb2.Chart,
) -> None:
    """Creates the Dashboard and its Chart through the Dashboard Service gRPC port."""
    print(f"--> Connecting to Dashboard Service gRPC at {dashboard_endpoint}...")
    with DashboardClient(endpoint=dashboard_endpoint) as client:
        created = client.create_dashboard(dashboard, dashboard_id=dashboard_id, charts=[chart])
        print(f"--> Created Dashboard successfully: {created.name}")
        print(f"--> Server-assigned chart IDs: {list(created.chart_ids)}")


def create_experiment_dashboard(dashboard_endpoint: str, xid: int, wid: int) -> None:
    """Builds and publishes the Dashboard and, in one transaction, the Chart it renders.

    Args:
        dashboard_endpoint: HTTP REST URL or gRPC target address of the Dashboard Service.
        xid: Experiment ID owning the work unit.
        wid: Work unit ID the chart belongs to.
    """
    dashboard_id = dashboard_id_for(xid, wid)
    dashboard = (
        DashboardBuilder(name=f"dashboards/{dashboard_id}")
        .set_title(f"{_EXPERIMENT_TITLE} {xid}")
        .set_description(f"Randomized training metrics for experiments/{xid}/workUnits/{wid}.")
        .set_grid_layout(columns=1)
        .build()
    )
    chart = build_training_chart(xid, wid)

    if dashboard_endpoint.startswith(("http://", "https://")):
        _publish_dashboard_over_rest(dashboard_endpoint, dashboard, dashboard_id, chart)
    else:
        _publish_dashboard_over_grpc(dashboard_endpoint, dashboard, dashboard_id, chart)


def create_chart_artifact(
    stub: experiment_state_api.ExperimentStateGRpcApi,
    work_unit_name: str,
    dashboard_url: str,
) -> artifact_pb2.Artifact:
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
                title=_CHART_TITLE,
                metric_names=list(METRIC_NAMES),
            )
        ),
    )
    request = ess_api_pb2.CreateArtifactRequest(parent=work_unit_name, artifact=artifact)
    return stub.create_artifact(request)


def create_dashboard_link_artifact(
    stub: experiment_state_api.ExperimentStateGRpcApi,
    work_unit_name: str,
    dashboard_url: str,
) -> artifact_pb2.Artifact:
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
    """Launches the experiment and provisions its chart, dashboard and artifacts."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
    # Worker-specific defaults; .env above wins, so a workstation override still applies.
    dotenv.load_dotenv(os.path.join(os.path.dirname(os.path.abspath(__file__)), "worker.env"))

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    image_path = _resolve_image_path()
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")
    ui_base_url = _resolve_ui_base_url()
    dashboard_endpoint = _resolve_dashboard_endpoint()
    create_dash = _get_env_bool("CREATE_DASHBOARD", default=True)
    seed = _resolve_seed()

    print("==================================================")
    print("  XMC Chart Artifact WorkUnit Launch Script       ")
    print("==================================================")
    print(f"Worker Image       : {image_path}")
    print(f"Queue Name         : {queue_name}")
    print(f"Dashboard Endpoint : {dashboard_endpoint}")
    print(f"Sampler Seed       : {seed}")
    print("==================================================")

    with xm_cloud.create_experiment(
        experiment_title=_EXPERIMENT_TITLE,
        # No `acls`: XMC makes you (the creator) the owner. See docs/authentication.md.
    ) as experiment:
        # pylint: disable=protected-access
        stub: experiment_state_api.ExperimentStateGRpcApi = experiment._experiment_state_stub
        xid = int(experiment.resource_name.split("/")[-1])
        print(f"\n[1/4] Created Experiment successfully: {experiment.resource_name} (XID: {xid})")

        created_wu = stub.create_work_unit(
            ess_api_pb2.CreateWorkUnitRequest(
                parent=experiment.resource_name,
                work_unit=build_work_unit(image_path, queue_name, build_worker_env(xid, seed)),
            )
        )
        # created_wu is an Operation named experiments/{xid}/workUnits/{wid}/operations/{op}.
        wid = int(created_wu.name.split("/")[3])
        work_unit_name = f"experiments/{xid}/workUnits/{wid}"
        print(f"[2/4] Created WorkUnit successfully: {work_unit_name} (WID: {wid})")

        dashboard_url = f"{ui_base_url}/dashboards/{dashboard_id_for(xid, wid)}"
        if create_dash:
            print("\n[3/4] Creating Dashboard and Chart for the generated metrics...")
            try:
                create_experiment_dashboard(dashboard_endpoint, xid, wid)
            except Exception as err:  # pylint: disable=broad-except
                sys.stderr.write(f"⚠️ Warning: Could not create dashboard: {err}\n")
                print("You can create the dashboard manually once the service is reachable.")
        else:
            print("\n[3/4] Skipping dashboard creation (CREATE_DASHBOARD=false).")

        print("\n[4/4] Attaching artifacts to the WorkUnit...")
        chart_artifact = create_chart_artifact(stub, work_unit_name, dashboard_url)
        print(f"--> Created Chart artifact: {chart_artifact.name}")
        link_artifact = create_dashboard_link_artifact(stub, work_unit_name, dashboard_url)
        print(f"--> Created Dashboard link artifact: {link_artifact.name}")

        print("\n==================================================")
        print("  🎉 Experiment and WorkUnit Launch Completed!    ")
        print(f"  Work Unit : {ui_base_url}/{work_unit_name}")
        print(f"  Dashboard : {dashboard_url}")
        print("==================================================")
        print("\nPlots appear once the worker has streamed its first metrics to ClickHouse.")


if __name__ == "__main__":
    app.run(main)
