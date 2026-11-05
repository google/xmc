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
import threading
import time
from collections.abc import Sequence
from typing import Any

try:
    import grpc
except ImportError:
    grpc = None

from xdash._protos import dashboard_service_pb2, messages_pb2
from xdash.auth import (
    LOCAL_HOSTS as _LOCAL_HOSTS,
    load_cached_cli_token,
    mint_iam_impersonated_token,
    resolve_env_auth_token,
)
from xdash.builders import chart_id_from_name

DEFAULT_DASHBOARD_ENDPOINT = "localhost:8080"
_TOKEN_TTL_SEC = 3300.0


def _fetch_iam_id_token(client_sa: str, audience: str) -> str:
    """Fetches an OIDC ID token via the IAM Credentials generateIdToken API.

    Args:
        client_sa: Service account email to impersonate.
        audience: Target OAuth client ID audience (IAP_CLIENT_ID).

    Returns:
        The signed OIDC ID token string.
    """
    try:
        import google.auth
        from google.auth.transport import requests as google_requests
    except ImportError as e:
        raise XDashError(
            "The 'google-auth' package is required for IAM OIDC token fallback. "
            "Please install it using 'pip install google-auth'."
        ) from e

    credentials, _ = google.auth.default()
    return mint_iam_impersonated_token(credentials, google_requests, client_sa, audience)


def _is_local_endpoint(endpoint: str) -> bool:
    """Reports whether a gRPC target addresses a local, plaintext service."""
    host = endpoint.rsplit(":", 1)[0] if ":" in endpoint else endpoint
    return not host or host.strip("[]").lower() in _LOCAL_HOSTS


def _reject_doubly_attached_charts(
    dashboard: messages_pb2.Dashboard,
    charts: Sequence[messages_pb2.Chart] | None,
) -> None:
    """Rejects a chart passed inline whose ID the dashboard also references.

    DashboardBuilder.add_chart derives a chart_id from a Chart message's name, which is the
    right thing to do for a chart that already exists and the wrong thing to do for one being
    created in the same call: the server assigns inline charts a fresh ID, so the derived one
    names nothing and the request fails with `chart "..." does not exist`. The two ways of
    attaching a chart look interchangeable at the call site, so the mistake is caught here
    rather than left to a server error that does not mention the inline chart at all.
    """
    if not charts or not dashboard.chart_ids:
        return
    referenced = set(dashboard.chart_ids)
    for chart in charts:
        if not chart.name:
            continue
        try:
            chart_id = chart_id_from_name(chart.name)
        except ValueError:
            # A name the server is about to overwrite anyway; nothing to cross-check.
            continue
        if chart_id in referenced:
            raise ValueError(
                f"chart {chart.name!r} is passed in charts and also referenced by "
                f"dashboard.chart_ids as {chart_id!r}. Charts created inline are named and "
                "identified by the server, so drop the DashboardBuilder.add_chart() call; "
                "use it only for charts returned by create_chart()."
            )


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
        self._cached_iam_token: tuple[tuple[str, str], str, float] | None = None
        self._token_lock = threading.Lock()
        env_ep = endpoint or os.environ.get("XMC_DASHBOARD_ENDPOINT")
        self._endpoint = (
            (env_ep or "")
            if (channel is not None and stub is None)
            else (env_ep or DEFAULT_DASHBOARD_ENDPOINT)
        )
        if secure is not None:
            self._secure = bool(secure)
        else:
            self._secure = credentials is not None or not _is_local_endpoint(self._endpoint)

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
            try:
                if self._secure:
                    self._channel = grpc.secure_channel(
                        self._endpoint, credentials or grpc.ssl_channel_credentials()
                    )
                else:
                    self._channel = grpc.insecure_channel(self._endpoint)
            except Exception as e:
                raise XDashConnectionError(
                    f"Failed to create gRPC channel to {self._endpoint}: {e}"
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

    def _resolve_auth_token(self) -> str:
        """Resolves the bearer token from env vars, cached CLI login, or IAM OIDC fallback."""
        try:
            env_token = resolve_env_auth_token()
        except ValueError as err:
            raise XDashError(str(err)) from err
        if env_token:
            return env_token
        if self._endpoint and (
            cli_token := load_cached_cli_token(self._endpoint, secure=self._secure)
        ):
            return cli_token
        client_sa = (os.environ.get("XMC_CLIENT_SA") or "").strip()
        audience = (os.environ.get("IAP_CLIENT_ID") or "").strip()
        if bool(client_sa) != bool(audience):
            raise XDashError(
                "Both XMC_CLIENT_SA and IAP_CLIENT_ID must be set for IAM OIDC fallback."
            )
        if not client_sa:
            return ""
        return self._get_cached_or_fetch_iam_token(client_sa, audience)

    def _get_valid_cached_token(self, key: tuple[str, str], now: float) -> str | None:
        """Returns a cached IAM token if present and not expired."""
        cached = self._cached_iam_token
        if cached is None:
            return None
        cached_key, cached_token, expires_at = cached
        if cached_key == key and now < expires_at:
            return cached_token
        return None

    def _get_cached_or_fetch_iam_token(self, client_sa: str, audience: str) -> str:
        """Returns a cached IAM OIDC token or mints a fresh one under a thread lock."""
        key = (client_sa, audience)
        cached_token = self._get_valid_cached_token(key, time.monotonic())
        if cached_token is not None:
            return cached_token
        with self._token_lock:
            now = time.monotonic()
            cached_token = self._get_valid_cached_token(key, now)
            if cached_token is not None:
                return cached_token
            fetched = _fetch_iam_id_token(client_sa, audience)
            self._cached_iam_token = (key, fetched, now + _TOKEN_TTL_SEC)
            return fetched

    def _execute_rpc(self, call: Any, request: Any, rpc_name: str) -> Any:
        """Executes a gRPC call and converts grpc.RpcError to XDashRPCError."""
        try:
            token = self._resolve_auth_token()
            if token:
                return call(request, metadata=(("authorization", f"Bearer {token}"),))
            return call(request)
        except XDashError:
            raise
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
        charts: Sequence[messages_pb2.Chart] | None = None,
    ) -> messages_pb2.Dashboard:
        """Creates a new dashboard via the Dashboard Service.

        Args:
            dashboard: The Dashboard protobuf message to create.
            dashboard_id: Optional ID override (extracted from name if not provided).
            charts: Charts to create in the same transaction as the dashboard. Each must
                carry the xid and wid of the work unit that owns it; the server names them,
                generates their IDs and appends those to the returned dashboard's chart_ids.
                A chart that already exists is attached through dashboard.chart_ids instead
                — passing it here would write a second copy of it.

        Returns:
            The created messages_pb2.Dashboard message.

        Raises:
            ValueError: If dashboard is unset, or if a chart is passed both inline and by ID.
        """
        if not dashboard:
            raise ValueError("dashboard message is required")
        if not dashboard_id and dashboard.name:
            # rstrip first: "dashboards/d1/" would otherwise derive an empty id, which the
            # server rejects with a message that points nowhere near the trailing slash.
            dashboard_id = dashboard.name.rstrip("/").split("/")[-1]
        _reject_doubly_attached_charts(dashboard, charts)
        req = dashboard_service_pb2.CreateDashboardRequest(
            dashboard=dashboard,
            dashboard_id=dashboard_id or "",
            charts=charts or [],
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
