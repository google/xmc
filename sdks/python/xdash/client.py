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
"""Python gRPC client for interacting with the XMC Dashboard Service."""

from __future__ import annotations

import os
from collections.abc import Sequence
from typing import Any

try:
    import grpc
except ImportError:
    grpc = None  # type: ignore[assignment]

from xdash._protos import dashboard_service_pb2, messages_pb2

DEFAULT_DASHBOARD_ENDPOINT = "localhost:8080"

# Hosts assumed to be a local development service, and therefore plaintext. Any other
# host — notably cloud.xmanager.googleapis.com — requires TLS.
_LOCAL_HOSTS = frozenset({"localhost", "127.0.0.1", "::1", "0.0.0.0"})


def _is_local_endpoint(endpoint: str) -> bool:
    """Reports whether a gRPC target addresses a local, plaintext service."""
    host = endpoint.rsplit(":", 1)[0] if ":" in endpoint else endpoint
    return host.strip("[]").lower() in _LOCAL_HOSTS


class XDashError(Exception):
    """Base exception class for all xdash errors."""


class XDashConnectionError(XDashError):
    """Raised when the client cannot establish a gRPC channel."""


class XDashRPCError(XDashError):
    """Raised when a gRPC call returns an error status code."""

    def __init__(self, message: str, code: Any | None = None) -> None:
        super().__init__(message)
        self.code = code


class DashboardServiceStub:
    """Lightweight gRPC stub for DashboardService RPC methods."""

    def __init__(self, channel: Any) -> None:
        if grpc is None:
            raise XDashConnectionError(
                "grpcio is not installed. Please install grpcio to connect to DashboardService."
            )
        self.CreateDashboard = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/CreateDashboard",
            request_serializer=dashboard_service_pb2.CreateDashboardRequest.SerializeToString,
            response_deserializer=messages_pb2.Dashboard.FromString,
        )
        self.GetDashboard = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/GetDashboard",
            request_serializer=dashboard_service_pb2.GetDashboardRequest.SerializeToString,
            response_deserializer=messages_pb2.Dashboard.FromString,
        )
        self.ListDashboards = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/ListDashboards",
            request_serializer=dashboard_service_pb2.ListDashboardsRequest.SerializeToString,
            response_deserializer=dashboard_service_pb2.ListDashboardsResponse.FromString,
        )
        self.CreateChart = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/CreateChart",
            request_serializer=dashboard_service_pb2.CreateChartRequest.SerializeToString,
            response_deserializer=messages_pb2.Chart.FromString,
        )
        self.GetChart = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/GetChart",
            request_serializer=dashboard_service_pb2.GetChartRequest.SerializeToString,
            response_deserializer=messages_pb2.Chart.FromString,
        )
        self.GetChartData = channel.unary_unary(
            "/cloud.xmanager.v1.DashboardService/GetChartData",
            request_serializer=dashboard_service_pb2.GetChartDataRequest.SerializeToString,
            response_deserializer=messages_pb2.ChartData.FromString,
        )


class DashboardClient:
    """Client for creating, retrieving, and inspecting XMC Dashboards and Charts."""

    def __init__(
        self,
        endpoint: str | None = None,
        channel: Any | None = None,
        stub: Any | None = None,
        secure: bool | None = None,
        credentials: Any | None = None,
    ) -> None:
        """Initializes the DashboardClient.

        Args:
            endpoint: Optional gRPC address (e.g. 'localhost:8080'). Defaults to
                XMC_DASHBOARD_ENDPOINT env var or 'localhost:8080'.
            channel: Optional pre-configured grpc.Channel. The caller keeps ownership of
                it; close() leaves an injected channel open.
            stub: Optional pre-configured or mock stub object for testing.
            secure: Whether to open a TLS channel. Defaults to True for every endpoint
                except a local one, since the hosted service requires TLS.
            credentials: Optional grpc.ChannelCredentials. Implies secure=True and
                replaces the default grpc.ssl_channel_credentials().

        Raises:
            ValueError: If credentials are supplied alongside secure=False.
        """
        # Only a channel this constructor opened is ours to close. An injected one may be
        # shared with other clients, and tearing it down under them is not our call.
        self._owns_channel = False

        if stub is not None:
            self._stub = stub
            self._channel = channel
            return

        if grpc is None:
            raise XDashConnectionError(
                "grpcio is not installed. Please install grpcio (pip install grpcio) to connect."
            )

        if credentials is not None and secure is False:
            raise ValueError("credentials cannot be combined with secure=False")

        if channel is not None:
            self._channel = channel
        else:
            resolved_endpoint = (
                endpoint or os.environ.get("XMC_DASHBOARD_ENDPOINT") or DEFAULT_DASHBOARD_ENDPOINT
            )
            if secure is None:
                secure = credentials is not None or not _is_local_endpoint(resolved_endpoint)
            try:
                if secure:
                    self._channel = grpc.secure_channel(
                        resolved_endpoint, credentials or grpc.ssl_channel_credentials()
                    )
                else:
                    self._channel = grpc.insecure_channel(resolved_endpoint)
            except Exception as e:
                raise XDashConnectionError(
                    f"Failed to create gRPC channel to {resolved_endpoint}: {e}"
                ) from e
            self._owns_channel = True

        self._stub = DashboardServiceStub(self._channel)

    def close(self) -> None:
        """Closes the gRPC channel, unless the caller injected one it owns itself."""
        if self._owns_channel and self._channel is not None:
            self._channel.close()

    def __enter__(self) -> DashboardClient:
        """Enters a context manager scope owning the gRPC channel."""
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: Any | None,
    ) -> None:
        """Releases the gRPC channel when leaving the context manager scope."""
        self.close()

    def _execute_rpc(self, call: Any, request: Any, rpc_name: str) -> Any:
        """Executes a gRPC call and converts grpc.RpcError to XDashRPCError."""
        try:
            return call(request)
        except Exception as err:
            if grpc is not None and isinstance(err, grpc.RpcError):
                code_fn = getattr(err, "code", None)
                code = code_fn() if callable(code_fn) else None
                details_fn = getattr(err, "details", None)
                details = details_fn() if callable(details_fn) else None
                # details() is empty on some statuses (a cancelled call, for one), and
                # "GetDashboard failed: None" tells the caller nothing.
                raise XDashRPCError(f"{rpc_name} failed: {details or err}", code=code) from err
            raise XDashError(f"Unexpected error in {rpc_name}: {err}") from err

    def create_dashboard(
        self,
        dashboard: messages_pb2.Dashboard,
        dashboard_id: str | None = None,
    ) -> messages_pb2.Dashboard:
        """Creates a new dashboard via the Dashboard Service.

        Args:
            dashboard: The Dashboard protobuf message to create.
            dashboard_id: Optional ID override (extracted from name if not provided).

        Returns:
            The created messages_pb2.Dashboard message.
        """
        if not dashboard:
            raise ValueError("dashboard message is required")
        if not dashboard_id and dashboard.name:
            # rstrip first: "dashboards/d1/" would otherwise derive an empty id, which the
            # server rejects with a message that points nowhere near the trailing slash.
            dashboard_id = dashboard.name.rstrip("/").split("/")[-1]
        req = dashboard_service_pb2.CreateDashboardRequest(
            dashboard=dashboard,
            dashboard_id=dashboard_id or "",
        )
        return self._execute_rpc(self._stub.CreateDashboard, req, "CreateDashboard")

    def get_dashboard(self, name: str) -> messages_pb2.Dashboard:
        """Retrieves a dashboard by its full resource name."""
        if not name or not name.strip():
            raise ValueError("Dashboard resource name is required")
        req = dashboard_service_pb2.GetDashboardRequest(name=name)
        return self._execute_rpc(self._stub.GetDashboard, req, "GetDashboard")

    def list_dashboards(self) -> list[messages_pb2.Dashboard]:
        """Lists all dashboards available in the system."""
        req = dashboard_service_pb2.ListDashboardsRequest()
        resp = self._execute_rpc(self._stub.ListDashboards, req, "ListDashboards")
        return list(resp.dashboards)

    def create_chart(
        self,
        parent: str,
        chart: messages_pb2.Chart,
        chart_id: str | None = None,
    ) -> messages_pb2.Chart:
        """Creates a new chart attached to an experiment work unit parent."""
        if not parent or not parent.strip():
            raise ValueError("parent resource name is required")
        if not chart:
            raise ValueError("chart message is required")
        if not chart_id and chart.name:
            # See create_dashboard: a trailing slash would otherwise derive an empty id.
            chart_id = chart.name.rstrip("/").split("/")[-1]
        req = dashboard_service_pb2.CreateChartRequest(
            parent=parent,
            chart=chart,
            chart_id=chart_id or "",
        )
        return self._execute_rpc(self._stub.CreateChart, req, "CreateChart")

    def create_charts(
        self,
        charts: Sequence[messages_pb2.Chart],
    ) -> list[messages_pb2.Chart]:
        """Creates each chart under the work unit parent derived from its own xid/wid.

        A Dashboard references charts by ID and never creates them, so charts must exist
        before CreateDashboard runs. This walks that first phase and hands back the
        server's versions, whose names carry the authoritative chart IDs.

        Args:
            charts: Chart messages to create, each with xid and wid set.

        Returns:
            The created charts, in the order supplied.

        Raises:
            ValueError: If a chart is None, or is missing a positive xid or wid, since
                the parent work unit cannot be derived without both.
        """
        created: list[messages_pb2.Chart] = []
        for chart in charts:
            # Guarded explicitly so a None in the sequence surfaces as the same
            # ValueError create_chart raises, not an AttributeError from chart.xid.
            if chart is None:
                raise ValueError("chart message is required and cannot be None")
            if chart.xid <= 0 or chart.wid <= 0:
                raise ValueError(
                    f"chart {chart.name or '<unnamed>'!r} needs a positive xid and wid to "
                    "derive its work unit parent; set them via ChartBuilder.for_experiment()"
                )
            parent = f"experiments/{chart.xid}/workUnits/{chart.wid}"
            created.append(self.create_chart(parent, chart))
        return created

    def get_chart(self, name: str) -> messages_pb2.Chart:
        """Retrieves a chart by its full resource name."""
        if not name or not name.strip():
            raise ValueError("Chart resource name is required")
        req = dashboard_service_pb2.GetChartRequest(name=name)
        return self._execute_rpc(self._stub.GetChart, req, "GetChart")

    def get_chart_data(self, name: str) -> messages_pb2.ChartData:
        """Fetches evaluated metrics data for a chart."""
        if not name or not name.strip():
            raise ValueError("Chart resource name is required")
        req = dashboard_service_pb2.GetChartDataRequest(name=name)
        return self._execute_rpc(self._stub.GetChartData, req, "GetChartData")
