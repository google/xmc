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

"""HTTP client for the Dashboard Service gRPC-transcoded REST endpoints.

The Dashboard Service gRPC port is cluster-internal. The deployment exposes only the
transcoded REST routes through the public gateway, so a launcher running outside the
cluster reaches it over HTTP with a bearer identity token.

Bodies and responses are plain JSON dictionaries; callers convert to and from protobuf
so this module stays free of generated-code import paths.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from typing import Any

_REQUEST_TIMEOUT_SECONDS = 30


class DashboardRestError(RuntimeError):
    """Raised when a Dashboard Service request fails or returns a non-2xx status."""


class DashboardRestClient:
    """Creates Charts and Dashboards over the Dashboard Service REST endpoints."""

    def __init__(self, base_url: str, identity_token: str) -> None:
        """Initialises the client.

        Args:
            base_url: Origin of the XMC UI host, e.g. `https://dev.example.com`.
            identity_token: OIDC token sent as the `Authorization: Bearer` credential.
        """
        self._base_url = base_url.rstrip("/")
        self._identity_token = identity_token

    def create_dashboard(
        self,
        dashboard: dict[str, Any],
        dashboard_id: str,
        charts: list[dict[str, Any]],
    ) -> dict[str, Any]:
        """Creates a Dashboard and its charts in a single transaction.

        The server assigns each inline chart a UUID and appends it to the returned
        dashboard's `chartIds`, so no separate `CreateChart` call is needed. Every chart
        must carry the `xid` and `wid` of the work unit that owns it.

        `CreateDashboard` transcodes with `body: "*"`, so the whole request message —
        not just the dashboard — forms the payload.

        Args:
            dashboard: JSON representation of the `Dashboard` message.
            dashboard_id: Resource ID slug for the dashboard; must not contain slashes.
            charts: JSON representations of the `Chart` messages to create with it.

        Returns:
            JSON representation of the created `Dashboard`.

        Raises:
            DashboardRestError: If the request fails or is rejected.
        """
        payload = {"dashboard": dashboard, "dashboardId": dashboard_id, "charts": charts}
        return self._post("/v1/dashboards", payload)

    def _post(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        """Posts a JSON body to the given path and decodes the JSON response."""
        request = urllib.request.Request(
            url=f"{self._base_url}{path}",
            data=json.dumps(body).encode("utf-8"),
            headers={
                "Authorization": f"Bearer {self._identity_token}",
                "Content-Type": "application/json",
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=_REQUEST_TIMEOUT_SECONDS) as response:
                return dict(json.loads(response.read().decode("utf-8")))
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            raise DashboardRestError(f"POST {path} returned HTTP {err.code}: {detail}") from err
        except urllib.error.URLError as err:
            raise DashboardRestError(f"POST {path} failed: {err.reason}") from err


def resource_id_from_name(resource_name: str) -> str:
    """Extracts the trailing resource ID from a resource name.

    Args:
        resource_name: Full resource name, e.g. `experiments/1/workUnits/2/charts/abc`.

    Returns:
        The final path segment, e.g. `abc`.

    Raises:
        ValueError: If the resource name is empty or ends with a slash.
    """
    resource_id = resource_name.rsplit("/", 1)[-1]
    if not resource_id:
        raise ValueError(f"resource name {resource_name!r} does not end with a resource ID")
    return resource_id
