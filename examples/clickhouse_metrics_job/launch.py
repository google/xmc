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

"""Launch script triggering an XMC WorkUnit that ingests metrics into ClickHouse and builds a Dashboard."""

from __future__ import annotations

import json
import logging
import os
import sys
import urllib.error
import urllib.request
from collections.abc import Sequence

import dotenv
from absl import app
from google.protobuf import json_format
from xdash import (
    USER_EMAIL_ENV_VAR,
    USER_EMAIL_METADATA_KEY,
    ChartBuilder,
    DashboardBuilder,
    DashboardClient,
    resolve_user_email,
    scatter_plot,
    summary_kpi_plot,
)
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


def _get_auth_headers() -> dict[str, str]:
    """Generates authentication and identity headers for HTTP requests to the XMC Gateway.

    Attempts to fetch a Google OIDC ID token using IAM credentials impersonation
    (XMC_CLIENT_SA and IAP_CLIENT_ID), direct id_token fetch, or an explicit
    AUTH_TOKEN/XMC_AUTH_TOKEN environment variable, and names the user the resulting
    dashboard should belong to.
    """
    headers = {"Content-Type": "application/json"}
    # Set before the token, so every path out of this function carries it. The token below is
    # minted for the impersonated service account, so this header is the only place the person
    # who ran the launcher appears; without it the Dashboard Service records the service account
    # as the owner, and only the service account can then administer the dashboard.
    if user_email := os.environ.get(USER_EMAIL_ENV_VAR) or resolve_user_email():
        headers[USER_EMAIL_METADATA_KEY] = user_email
    else:
        sys.stderr.write(
            "⚠️ Warning: could not determine the calling user's email; the dashboard will be "
            f"owned by the authenticated principal. Set {USER_EMAIL_ENV_VAR} to name the user.\n"
        )

    if auth_token := os.environ.get("XMC_AUTH_TOKEN") or os.environ.get("AUTH_TOKEN"):
        headers["Authorization"] = f"Bearer {auth_token}"
        return headers

    iap_client_id = os.environ.get("IAP_CLIENT_ID") or os.environ.get("CLIENT_ID")
    client_sa = os.environ.get("XMC_CLIENT_SA")

    if not iap_client_id:
        return headers

    try:
        import google.auth
        from google.auth.transport import requests as google_requests

        credentials, _ = google.auth.default()
        if client_sa:
            authed_session = google_requests.AuthorizedSession(credentials)
            url = (
                "https://iamcredentials.googleapis.com/v1/projects/-/"
                f"serviceAccounts/{client_sa}:generateIdToken"
            )
            body = {"audience": iap_client_id, "includeEmail": True}
            response = authed_session.post(url, json=body, timeout=10)
            response.raise_for_status()
            token = response.json().get("token")
            if token:
                headers["Authorization"] = f"Bearer {token}"
                return headers
        else:
            from google.oauth2 import id_token

            req = google_requests.Request()
            token = id_token.fetch_id_token(req, iap_client_id)
            if token:
                headers["Authorization"] = f"Bearer {token}"
                return headers
    except Exception as err:  # pylint: disable=broad-except
        sys.stderr.write(f"⚠️ Warning: Could not retrieve OIDC ID token: {err}\n")

    return headers


def create_experiment_dashboard(
    dashboard_endpoint: str,
    xid: int,
    wid: int,
) -> None:
    """Builds and publishes an XMC dashboard and chart for the experiment metrics.

    Args:
        dashboard_endpoint: HTTP REST URL or gRPC target address of the Dashboard Service.
        xid: Experiment ID.
        wid: WorkUnit ID.
    """
    # Deliberately unnamed: this chart is created inline with the dashboard, so the server names
    # it, generates its ID and appends that ID to dashboard.chart_ids. A name set here would be
    # overwritten, and deriving a chart_id from it -- which is what DashboardBuilder.add_chart does
    # -- would declare a reference to a chart that is supposed to already exist, which the server
    # rejects with `chart "..." does not exist`.
    chart = (
        ChartBuilder()
        .for_experiment(xid=xid, wid=wid)
        .set_title("Model Training & Evaluation Metrics")
        .set_description(f"Performance and convergence metrics for Experiment {xid} WorkUnit {wid}")
        .set_grid_layout(columns=2)
        .add_plot(
            summary_kpi_plot(
                "loss_kpi",
                "Loss KPI (Latest)",
                ["train/loss", "eval/loss"],
            )
        )
        .add_plot(
            summary_kpi_plot(
                "acc_kpi",
                "Accuracy KPI (Latest)",
                ["train/accuracy"],
            )
        )
        .add_plot(
            scatter_plot(
                "loss_curves",
                "Loss Curves vs Step",
                ["train/loss", "eval/loss"],
            )
        )
        .add_plot(
            scatter_plot(
                "accuracy_curve",
                "Training Accuracy vs Step",
                ["train/accuracy"],
            )
        )
        .add_plot(
            scatter_plot(
                "step_time_curve",
                "Step Execution Time (ms)",
                ["perf/step_time_ms"],
            )
        )
        .build()
    )

    dashboard_id = f"exp_{xid}_training_overview"
    dashboard = (
        DashboardBuilder(name=f"dashboards/{dashboard_id}")
        .set_title(f"Experiment {xid} - Training & Metrics Overview")
        .set_description(f"Automated monitoring dashboard for Experiment {xid} (WorkUnit {wid}).")
        .set_grid_layout(columns=1)
        .build()
    )

    if dashboard_endpoint.startswith("http://") or dashboard_endpoint.startswith("https://"):
        print(
            f"--> Publishing Dashboard '{dashboard.title}' to HTTP REST endpoint {dashboard_endpoint}..."
        )
        chart_dict = json_format.MessageToDict(chart, preserving_proto_field_name=False)
        dashboard_dict = json_format.MessageToDict(dashboard, preserving_proto_field_name=False)
        payload = {
            "dashboard": dashboard_dict,
            "dashboard_id": dashboard_id,
            "charts": [chart_dict],
        }
        req_data = json.dumps(payload).encode("utf-8")
        headers = _get_auth_headers()
        req = urllib.request.Request(
            dashboard_endpoint,
            data=req_data,
            headers=headers,
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
    else:
        print(f"--> Connecting to Dashboard Service gRPC at {dashboard_endpoint}...")
        with DashboardClient(endpoint=dashboard_endpoint) as client:
            print(f"--> Creating Dashboard '{dashboard.title}' (ID: {dashboard_id})...")
            created_dashboard = client.create_dashboard(
                dashboard, dashboard_id=dashboard_id, charts=[chart]
            )
            print(f"--> Created Dashboard successfully: {created_dashboard.name}")
            print(f"--> Server-assigned chart IDs: {list(created_dashboard.chart_ids)}")


def main(argv: Sequence[str]) -> None:
    """Launches ClickHouse metrics logging experiment and configures its dashboard."""
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()
    # Load worker-specific environment defaults if present
    script_dir = os.path.dirname(os.path.abspath(__file__))
    worker_env_path = os.path.join(script_dir, "worker.env")
    if os.path.exists(worker_env_path):
        dotenv.load_dotenv(worker_env_path)

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    project_id = os.environ.get("PROJECT_ID", "xmc-test-project-6")
    image_path = os.environ.get(
        "IMAGE_PATH",
        f"us-docker.pkg.dev/{project_id}/xmc/clickhouse-metrics-job:latest",
    )
    queue_name = os.environ.get("QUEUE_NAME", "cpu-queue")

    # ClickHouse connection parameters inside cluster
    ch_host = os.environ.get(
        "CLICKHOUSE_HOST",
        "clickhouse.xmc-metrics.svc.cluster.local",
    )
    ch_port = os.environ.get("CLICKHOUSE_PORT", "8123")
    ch_user = os.environ.get("CLICKHOUSE_USER", "default")
    ch_password = os.environ.get("CLICKHOUSE_PASSWORD", "")
    ch_database = os.environ.get("CLICKHOUSE_DATABASE", "default")
    ch_secure = os.environ.get("CLICKHOUSE_SECURE", "false")
    ch_table = os.environ.get("CLICKHOUSE_TABLE", "xmc_metrics")
    num_steps = os.environ.get("NUM_STEPS", "100")

    # Dashboard service endpoint derived from environment (XMANAGER_ENDPOINT / XMANAGER_UI_URL)
    dashboard_endpoint = _resolve_dashboard_endpoint()
    create_dash = _get_env_bool("CREATE_DASHBOARD", default=True)

    print("==================================================")
    print("  XMC ClickHouse Metrics WorkUnit Launch Script  ")
    print("==================================================")
    print(f"Worker Image       : {image_path}")
    print(f"Queue Name         : {queue_name}")
    print(f"ClickHouse Host    : {ch_host}:{ch_port}")
    print(f"ClickHouse Database: {ch_database}")
    print(f"Dashboard Endpoint : {dashboard_endpoint}")
    print("==================================================")

    with xm_cloud.create_experiment(
        experiment_title="ClickHouse Metrics Ingestion Experiment",
        acls=xid_messages_pb2.XidAcls(
            owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
        ),
    ) as experiment:
        stub = experiment._experiment_state_stub  # pylint: disable=protected-access
        xid = int(experiment.resource_name.split("/")[-1])
        print(f"\n[1/3] Created Experiment successfully: {experiment.resource_name} (XID: {xid})")

        # WorkUnit environment variables passing ClickHouse configuration
        worker_env = [
            work_unit_pb2.EnvVar(name="CLICKHOUSE_HOST", value=ch_host),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_PORT", value=ch_port),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_USER", value=ch_user),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_PASSWORD", value=ch_password),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_DATABASE", value=ch_database),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_SECURE", value=ch_secure),
            work_unit_pb2.EnvVar(name="CLICKHOUSE_TABLE", value=ch_table),
            work_unit_pb2.EnvVar(name="NUM_STEPS", value=num_steps),
        ]

        worker_resources = resources_pb2.ResourceSet(
            cpu_resources=resources_pb2.CpuResources(
                cpu_millicores=100,
                memory_bytes=256 * 1024 * 1024,
            ),
        )

        work_unit = work_unit_pb2.WorkUnit(
            work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
            priority=priority_pb2.Priority.NORMAL,
            notes="WorkUnit ingesting DataEngine metrics directly into ClickHouse.",
            tags=["metrics-test", "clickhouse", "dataengine"],
            executable=work_unit_pb2.Executable(
                scheduling_constraints=work_unit_pb2.SchedulingConstraints(queue_name=queue_name),
                jobset=work_unit_pb2.JobSetExecutable(
                    jobs=[
                        work_unit_pb2.KubernetesJob(
                            name="clickhouse-metric-writer",
                            replicas=1,
                            spec=work_unit_pb2.ExecutableSpec(
                                name="clickhouse-metric-writer",
                                docker_image=image_path,
                                command=["python3", "-u", "/app/worker.py"],
                                resources=worker_resources,
                                env=worker_env,
                            ),
                        ),
                    ]
                ),
            ),
        )

        create_req = ess_api_pb2.CreateWorkUnitRequest(
            parent=experiment.resource_name,
            work_unit=work_unit,
        )
        created_wu = stub.create_work_unit(create_req)
        # Parse work unit ID from resource name "experiments/{xid}/workUnits/{wid}"
        raw_wid = created_wu.name.split("/")[-1]
        wid = int(raw_wid) if raw_wid.isdigit() else 1
        print(f"[2/3] Created WorkUnit successfully: {created_wu.name} (WID: {wid})")

        if create_dash:
            print("\n[3/3] Creating Dashboard and Chart for generated metrics...")
            try:
                create_experiment_dashboard(
                    dashboard_endpoint=dashboard_endpoint,
                    xid=xid,
                    wid=wid,
                )
            except Exception as err:  # pylint: disable=broad-except
                sys.stderr.write(f"⚠️ Warning: Could not create dashboard: {err}\n")
                print(
                    "You can create the dashboard manually once the Dashboard Service is running."
                )
        else:
            print("\n[3/3] Skipping dashboard creation (CREATE_DASHBOARD=false).")

        print("\n==================================================")
        print("  🎉 Experiment and WorkUnit Launch Completed!    ")
        print(f"  Experiment XID : {xid}")
        print(f"  WorkUnit ID    : {wid}")
        print("==================================================")


if __name__ == "__main__":
    app.run(main)
