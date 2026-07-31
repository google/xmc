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

"""Dashboard, Chart, and Plot wrappers for XManager on Cloud."""

import functools
import os
from collections.abc import Sequence
from typing import Any

import grpc


class _DummyProtoMessage:
    """Dummy proto message class for fallback when protos are not injected."""

    def __init__(self, **kwargs: Any) -> None:
        for k, v in kwargs.items():
            setattr(self, k, v)


class _DummyProto:
    """Dummy proto module for fallback when protos are not injected."""

    # pylint: disable=invalid-name
    CreateDashboardRequest: Any = _DummyProtoMessage
    GetDashboardRequest: Any = _DummyProtoMessage
    ListDashboardsRequest: Any = _DummyProtoMessage
    UpdateDashboardRequest: Any = _DummyProtoMessage
    DashboardServiceStub: Any = _DummyProtoMessage
    Dashboard: Any = _DummyProtoMessage
    Chart: Any = _DummyProtoMessage
    Plot: Any = _DummyProtoMessage

    def __getattr__(self, name: str) -> Any:
        return _DummyProtoMessage


# Injected protobuf dependencies from github.com/google/xmc repo.
# Specifically from api/protos/dashboard. During local builds before
# present, so we fallback gracefully for analysis and testing.
try:
    from xmanager_cloud.dashboard_service.proto import (
        dashboard_service_pb2 as api_pb2,
        dashboard_service_pb2_grpc as api_pb2_grpc,
        messages_pb2 as dashboard_pb2,
    )
except ImportError:
    api_pb2 = _DummyProto()
    api_pb2_grpc = _DummyProto()
    dashboard_pb2 = _DummyProto()


class _AuditMetadataInterceptor(grpc.UnaryUnaryClientInterceptor):
    """gRPC client interceptor to audit user email metadata."""

    def __init__(self, user_email: str) -> None:
        self._user_email = user_email

    def intercept_unary_unary(
        self,
        continuation: Any,
        client_call_details: Any,
        request: Any,
    ) -> Any:
        metadata = list(client_call_details.metadata or [])
        metadata.append(("x-goog-user-email", self._user_email))
        return continuation(
            client_call_details._replace(metadata=metadata),
            request,
        )


def _fetch_oidc_token(iap_client_id: str, client_sa: str) -> str:
    """Fetches an OpenID Connect token for IAP authentication via service account impersonation."""
    import google.auth
    import google.auth.transport.requests

    credentials, _ = google.auth.default()
    authed_session = google.auth.transport.requests.AuthorizedSession(credentials)
    url = (
        "https://iamcredentials.googleapis.com/v1/projects/-/"
        f"serviceAccounts/{client_sa}:generateIdToken"
    )
    body = {"audience": iap_client_id, "includeEmail": True}
    response = authed_session.post(url, json=body)
    response.raise_for_status()
    return str(response.json()["token"])


def _create_authenticated_channel(
    endpoint: str,
    iap_client_id: str,
    client_sa: str,
) -> grpc.Channel:
    """Creates an authenticated secure gRPC channel using an IAP OIDC token."""
    import google.auth

    open_id_connect_token = _fetch_oidc_token(iap_client_id, client_sa)
    user_email = ""
    try:
        from xmanager_cloud.experiment_state_server import experiment_state_api

        user_email = experiment_state_api.get_current_user_email()
    except Exception:  # pylint: disable=broad-except
        credentials, _ = google.auth.default()
        user_email = getattr(credentials, "service_account_email", "")

    channel_creds = grpc.ssl_channel_credentials()
    call_creds = grpc.access_token_call_credentials(open_id_connect_token)
    composite_creds = grpc.composite_channel_credentials(channel_creds, call_creds)
    channel = grpc.secure_channel(endpoint, composite_creds)

    if user_email:
        interceptor = _AuditMetadataInterceptor(user_email)
        channel = grpc.intercept_channel(channel, interceptor)

    return channel


@functools.lru_cache
def get_dashboard_service_stub() -> Any:
    """Returns a cached gRPC stub for DashboardService, with optional IAP authentication."""
    endpoint = os.environ.get("XMANAGER_DASHBOARD_ENDPOINT", "localhost:8080")
    iap_client_id = os.environ.get("IAP_CLIENT_ID")
    client_sa = os.environ.get("XMC_CLIENT_SA")

    if iap_client_id and client_sa:
        channel = _create_authenticated_channel(endpoint, iap_client_id, client_sa)
    else:
        channel = grpc.insecure_channel(endpoint)

    return api_pb2_grpc.DashboardServiceStub(channel)


class Plot:
    """A plot visualization inside a chart."""

    def __init__(self, plot_proto: Any) -> None:
        self._plot_proto = plot_proto

    @property
    def title(self) -> str:
        """The title of the plot."""
        return getattr(self._plot_proto, "title", "")

    def to_proto(self) -> Any:
        """Returns the underlying protobuf message."""
        return self._plot_proto


class Chart:
    """A chart inside a dashboard, referencing exactly 1 experiment and containing Plots."""

    def __init__(self, chart_proto: Any) -> None:
        self._chart_proto = chart_proto

    @property
    def title(self) -> str:
        """The title of the chart."""
        return getattr(self._chart_proto, "title", "")

    @property
    def experiment_id(self) -> int:
        """The XID of the experiment referenced by this chart."""
        return getattr(self._chart_proto, "experiment_id", 0)

    @property
    def experiment_name(self) -> str:
        """The full resource name of the experiment referenced by this chart."""
        return f"experiments/{self.experiment_id}"

    @property
    def plots(self) -> Sequence[Plot]:
        """The list of plots contained in this chart."""
        plots_list = getattr(self._chart_proto, "plots", [])
        return [Plot(plot_proto) for plot_proto in plots_list]

    def to_proto(self) -> Any:
        """Returns the underlying protobuf message."""
        return self._chart_proto


class Dashboard:
    """A standalone dashboard resource containing one or more Charts."""

    def __init__(
        self,
        dashboard_proto: Any,
        stub: Any | None = None,
    ) -> None:
        self._dashboard_proto = dashboard_proto
        self._stub = stub or get_dashboard_service_stub()

    @property
    def name(self) -> str:
        """The standalone resource name of the dashboard (e.g. dashboards/123)."""
        return getattr(self._dashboard_proto, "name", "")

    @property
    def id(self) -> str:
        """The unique ID of the dashboard."""
        return self.name.split("/")[-1] if self.name else ""

    @property
    def title(self) -> str:
        """The display title of the dashboard."""
        return getattr(self._dashboard_proto, "title", "")

    @property
    def charts(self) -> Sequence[Chart]:
        """The list of charts contained in this dashboard."""
        charts_list = getattr(self._dashboard_proto, "charts", [])
        return [Chart(chart_proto) for chart_proto in charts_list]

    def update(self, **kwargs: Any) -> None:
        """Updates fields on the dashboard and saves via gRPC."""
        for key, value in kwargs.items():
            if hasattr(self._dashboard_proto, key):
                setattr(self._dashboard_proto, key, value)
        request = api_pb2.UpdateDashboardRequest(dashboard=self._dashboard_proto)
        self._dashboard_proto = self._stub.UpdateDashboard(request)


def create_plot(title: str, **kwargs: Any) -> Plot:
    """Creates a new Plot wrapper.

    Args:
        title: The title of the plot.
        **kwargs: Additional fields on the PlotSpec protobuf message.

    Returns:
        The created Plot wrapper instance.
    """
    proto = dashboard_pb2.PlotSpec(title=title, **kwargs)
    return Plot(proto)


def create_chart(
    title: str,
    experiment_id: int,
    plots: Sequence[Plot | Any],
    **kwargs: Any,
) -> Chart:
    """Creates a new Chart wrapper referencing exactly 1 experiment.

    Args:
        title: Display title for the chart.
        experiment_id: XID of the experiment referenced by this chart.
        plots: Sequence of plots to include in the chart.
        **kwargs: Additional fields on the Chart protobuf message.

    Returns:
        The created Chart wrapper instance.

    Raises:
        ValueError: If plots is empty.
    """
    if not plots:
        raise ValueError("A Chart must contain at least one Plot.")
    plot_protos = [p.to_proto() if isinstance(p, Plot) else p for p in plots]
    proto = dashboard_pb2.Chart(
        title=title,
        experiment_id=experiment_id,
        plots=plot_protos,
        **kwargs,
    )
    return Chart(proto)


def create_dashboard(
    title: str,
    charts: Sequence[Chart | Any],
    **kwargs: Any,
) -> Dashboard:
    """Creates a new standalone dashboard resource.

    Args:
        title: Display title for the dashboard.
        charts: One or more charts to include in the dashboard. Each chart must
            reference exactly 1 experiment.
        **kwargs: Additional fields on the Dashboard protobuf message.

    Returns:
        The created Dashboard wrapper instance.

    Raises:
        ValueError: If charts is empty.
    """
    if not charts:
        raise ValueError("A dashboard must contain at least one Chart.")

    chart_protos = [c.to_proto() if isinstance(c, Chart) else c for c in charts]
    proto = dashboard_pb2.Dashboard(
        title=title,
        charts=chart_protos,
        **kwargs,
    )
    request = api_pb2.CreateDashboardRequest(dashboard=proto)
    stub = get_dashboard_service_stub()
    created_proto = stub.CreateDashboard(request)
    return Dashboard(created_proto, stub=stub)


def get_dashboard(name: str) -> Dashboard:
    """Retrieves an existing dashboard by its resource name (e.g. dashboards/123).

    Args:
        name: The standalone resource name of the dashboard.

    Returns:
        The retrieved Dashboard wrapper instance.
    """
    stub = get_dashboard_service_stub()
    request = api_pb2.GetDashboardRequest(name=name)
    return Dashboard(stub.GetDashboard(request), stub=stub)


def list_dashboards(*, filter_query: str | None = None) -> Sequence[Dashboard]:
    """Lists dashboards, optionally filtered by AIP-132 filter query.

    Args:
        filter_query: Optional AIP-132 filter query string.

    Returns:
        A sequence of matching Dashboard wrapper instances.
    """
    stub = get_dashboard_service_stub()
    request = api_pb2.ListDashboardsRequest(filter=filter_query or "")
    response = stub.ListDashboards(request)
    return [Dashboard(proto, stub=stub) for proto in response.dashboards]
