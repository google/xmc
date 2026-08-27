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

"""Launches an experiment whose WorkUnit streams metrics, then charts it end to end.

One script covers the whole loop: create the Experiment, create a WorkUnit whose worker
logs through the XMC SDK to the ingestion gateway, then register Charts and a Dashboard
against the XID and WID the platform just handed back. Nothing here touches the metrics
store -- the dashboard describes which metrics to plot, and the Dashboard Service
resolves them on demand when the charts are viewed.
"""

from __future__ import annotations

import logging
import os
import sys
from collections.abc import Sequence

import dotenv
from absl import app
from metric_names import (
    EVAL_ACCURACY,
    EVAL_LOSS,
    STEP_TIME_MS,
    TRAIN_ACCURACY,
    TRAIN_LOSS,
)
from xdash import ChartBuilder, DashboardBuilder, DashboardClient, scatter_plot, summary_kpi_plot
from xmanager import xm_cloud
from xmanager.xm_cloud import snapshot_code
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as ess_api_pb2,
    priority_pb2,
    resources_pb2,
    work_unit_pb2,
)
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2

# Skip git push snapshot during local test launch
snapshot_code.push_repo_state = lambda *args, **kwargs: type("Dummy", (), {"commit_url": None})()

DEFAULT_GATEWAY_URL = "http://ingestion-gateway-service.xmc.svc.cluster.local:8080/api/v1/metrics"


def _get_env_bool(key: str, default: bool = True) -> bool:
    """Parses a boolean value from environment variables."""
    val = os.environ.get(key, "").strip().lower()
    if not val:
        return default
    return val in {"1", "true", "yes", "t", "y"}


def build_charts(xid: int, wid: int) -> list:
    """Builds the charts describing the metrics this experiment's worker emits.

    Two charts rather than one, so the dashboard demonstrates composition: quality
    metrics and throughput answer different questions and are read at different times.

    Args:
        xid: Experiment ID the charts query.
        wid: WorkUnit ID the charts query.

    Returns:
        Chart protobuf messages, not yet created on the server.
    """
    training = (
        ChartBuilder(name=f"experiments/{xid}/workUnits/{wid}/charts/training_progress")
        .for_experiment(xid=xid, wid=wid)
        .set_title("Training Progress")
        .set_description(f"Loss and accuracy for Experiment {xid} WorkUnit {wid}.")
        .set_grid_layout(columns=2)
        .add_plot(summary_kpi_plot("loss_kpi", "Loss (latest)", [TRAIN_LOSS, EVAL_LOSS]))
        .add_plot(
            summary_kpi_plot("accuracy_kpi", "Accuracy (latest)", [TRAIN_ACCURACY, EVAL_ACCURACY])
        )
        .add_plot(scatter_plot("loss_curves", "Loss vs Step", [TRAIN_LOSS, EVAL_LOSS]))
        .add_plot(
            scatter_plot("accuracy_curves", "Accuracy vs Step", [TRAIN_ACCURACY, EVAL_ACCURACY])
        )
        .build()
    )

    throughput = (
        ChartBuilder(name=f"experiments/{xid}/workUnits/{wid}/charts/throughput")
        .for_experiment(xid=xid, wid=wid)
        .set_title("Throughput")
        .set_description(f"Step execution time for Experiment {xid} WorkUnit {wid}.")
        .set_grid_layout(columns=1)
        .add_plot(summary_kpi_plot("step_time_kpi", "Step time, ms (latest)", [STEP_TIME_MS]))
        .add_plot(scatter_plot("step_time_curve", "Step Time (ms) vs Step", [STEP_TIME_MS]))
        .build()
    )

    return [training, throughput]


def create_experiment_dashboard(dashboard_endpoint: str, xid: int, wid: int) -> None:
    """Registers the charts and the dashboard that references them.

    Args:
        dashboard_endpoint: gRPC target address of the Dashboard Service.
        xid: Experiment ID.
        wid: WorkUnit ID.
    """
    print(f"--> Connecting to Dashboard Service at {dashboard_endpoint}...")
    with DashboardClient(endpoint=dashboard_endpoint) as client:
        charts = build_charts(xid=xid, wid=wid)

        # Charts first: a Dashboard references charts by ID and never creates them, so
        # they have to exist before CreateDashboard runs. The server's copies are what
        # the dashboard must reference, because their names carry the authoritative
        # chart IDs, which need not match the ones requested here.
        print(f"--> Creating {len(charts)} Charts...")
        created_charts = client.create_charts(charts)
        for chart in created_charts:
            print(f"    created {chart.name}")

        dashboard_id = f"exp_{xid}_overview"
        builder = (
            DashboardBuilder(name=f"dashboards/{dashboard_id}")
            .set_title(f"Experiment {xid} - Metrics Overview")
            .set_description(
                f"Metrics streamed from Experiment {xid} WorkUnit {wid} via the ingestion gateway."
            )
            .set_grid_layout(columns=1)
        )
        for chart in created_charts:
            builder.add_chart(chart)

        print(f"--> Creating Dashboard (ID: {dashboard_id})...")
        created = client.create_dashboard(builder.build(), dashboard_id=dashboard_id)
        print(f"--> Created Dashboard successfully: {created.name}")


def build_work_unit(image_path: str, queue_name: str, worker_env: list) -> work_unit_pb2.WorkUnit:
    """Builds the single-job WorkUnit that runs the metrics worker.

    Args:
        image_path: Container image holding worker.py.
        queue_name: Target Kueue queue.
        worker_env: Environment variables passed to the worker container.

    Returns:
        The WorkUnit message to create.
    """
    resources = resources_pb2.ResourceSet(
        cpu_resources=resources_pb2.CpuResources(
            cpu_millicores=500,
            memory_bytes=512 * 1024 * 1024,
        ),
    )

    return work_unit_pb2.WorkUnit(
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.NORMAL,
        notes="WorkUnit streaming training metrics to the ingestion gateway for a dashboard.",
        tags=["metrics", "dashboard", "end-to-end"],
        executable=work_unit_pb2.Executable(
            scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
            jobset=work_unit_pb2.JobSetExecutable(
                jobs=[
                    work_unit_pb2.KubernetesJob(
                        name="metrics-worker",
                        replicas=1,
                        spec=work_unit_pb2.ExecutableSpec(
                            name="metrics-worker",
                            docker_image=image_path,
                            command=["python3", "-u", "/app/worker.py"],
                            resources=resources,
                            env=worker_env,
                        ),
                    ),
                ]
            ),
        ),
    )


def main(argv: Sequence[str]) -> None:
    """Creates the experiment, launches the metrics WorkUnit and builds its dashboard."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID", "xmc-test-project-6")
    image_path = os.environ.get(
        "IMAGE_PATH",
        f"us-docker.pkg.dev/{project_id}/xmc/metrics-dashboard-job:latest",
    )
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")
    gateway_url = os.environ.get("XMC_GATEWAY_URL", DEFAULT_GATEWAY_URL)
    dashboard_endpoint = os.environ.get("XMC_DASHBOARD_ENDPOINT", "localhost:50051")
    num_steps = os.environ.get("NUM_STEPS", "100")
    create_dash = _get_env_bool("CREATE_DASHBOARD", default=True)

    print("==================================================")
    print("  XMC Metrics + Dashboard End-to-End Launch")
    print("==================================================")
    print(f"Worker Image       : {image_path}")
    print(f"Queue Name         : {queue_name}")
    print(f"Ingestion Gateway  : {gateway_url}")
    print(f"Dashboard Endpoint : {dashboard_endpoint}")
    print(f"Simulated Steps    : {num_steps}")
    print("==================================================")

    with xm_cloud.create_experiment(
        experiment_title="Metrics Dashboard End-to-End Experiment",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = int(experiment.resource_name.split("/")[-1])
        print(f"\n[1/3] Created Experiment: {experiment.resource_name} (XID: {xid})")

        # XMC_WORK_UNIT_ID, XMC_EXECUTION_ID and XMC_JOB_NAME are injected into the pod
        # by the platform from its own labels, so they are deliberately absent here.
        # XMC_XID is not: the platform publishes the experiment as XMC_EXPERIMENT_ID,
        # and the SDK reads XMC_XID, so this is the one identifier the launcher owns.
        worker_env = [
            work_unit_pb2.EnvVar(name="XMC_GATEWAY_URL", value=gateway_url),
            work_unit_pb2.EnvVar(name="XMC_XID", value=str(xid)),
            work_unit_pb2.EnvVar(name="NUM_STEPS", value=num_steps),
        ]

        created_wu = stub.create_work_unit(
            ess_api_pb2.CreateWorkUnitRequest(
                parent=experiment.resource_name,
                work_unit=build_work_unit(image_path, queue_name, worker_env),
            )
        )
        # Parsed the same way as the xid above, with no fallback. A name that does not end
        # in the WorkUnit ID means the create call did not return what the API promises,
        # and defaulting to a fixed id would bind the charts to some *other* WorkUnit --
        # producing a dashboard that looks healthy while plotting the wrong run.
        wid = int(created_wu.name.split("/")[-1])
        print(f"[2/3] Created WorkUnit: {created_wu.name} (WID: {wid})")

        if not create_dash:
            print("\n[3/3] Skipping dashboard creation (CREATE_DASHBOARD=false).")
        else:
            print("\n[3/3] Creating Charts and Dashboard...")
            try:
                create_experiment_dashboard(dashboard_endpoint, xid=xid, wid=wid)
            except Exception as err:  # pylint: disable=broad-except
                # The WorkUnit is already running and will keep streaming metrics, so a
                # dashboard failure is not worth failing the launch over -- the charts
                # can be recreated against the XID printed below at any time.
                sys.stderr.write(f"⚠️  Could not create dashboard: {err}\n")
                print("Re-run the dashboard step once the Dashboard Service is reachable.")

        print("\n==================================================")
        print("  🎉 Launch completed")
        print(f"  Experiment XID : {xid}")
        print(f"  WorkUnit ID    : {wid}")
        print("  Charts populate as the worker streams metrics.")
        print("==================================================")


if __name__ == "__main__":
    app.run(main)
