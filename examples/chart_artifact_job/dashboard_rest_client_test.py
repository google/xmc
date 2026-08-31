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

"""Unit tests for the Dashboard Service REST client."""

import io
import json
import unittest
import urllib.error
from unittest import mock

from dashboard_rest_client import (
    DashboardRestClient,
    DashboardRestError,
    resource_id_from_name,
)

_BASE_URL = "https://dev.example.com"
_TOKEN = "test-identity-token"


def _json_response(payload):
    """Builds a context-manager response double returning the given JSON payload."""
    response = mock.MagicMock()
    response.read.return_value = json.dumps(payload).encode("utf-8")
    response.__enter__.return_value = response
    return response


def _http_error(code, body):
    """Builds an HTTPError whose body is readable exactly like a real one."""
    return urllib.error.HTTPError(
        url=_BASE_URL, code=code, msg="error", hdrs=None, fp=io.BytesIO(body.encode("utf-8"))
    )


class DashboardRestClientTest(unittest.TestCase):
    """Covers URL construction, request shape and error translation."""

    def setUp(self):
        super().setUp()
        self.client = DashboardRestClient(base_url=f"{_BASE_URL}/", identity_token=_TOKEN)
        urlopen_patcher = mock.patch("dashboard_rest_client.urllib.request.urlopen")
        self.urlopen = urlopen_patcher.start()
        self.addCleanup(urlopen_patcher.stop)

    def _sent_request(self):
        """Returns the Request object handed to urlopen by the call under test."""
        return self.urlopen.call_args.args[0]

    def test_create_dashboard_wraps_the_body_in_the_request_message(self):
        self.urlopen.return_value = _json_response({"name": "dashboards/d1"})

        created = self.client.create_dashboard(
            {"title": "Run"}, dashboard_id="d1", charts=[{"title": "Loss"}]
        )

        request = self._sent_request()
        self.assertEqual(request.full_url, f"{_BASE_URL}/v1/dashboards")
        self.assertEqual(request.method, "POST")
        self.assertEqual(
            json.loads(request.data),
            {
                "dashboard": {"title": "Run"},
                "dashboardId": "d1",
                "charts": [{"title": "Loss"}],
            },
        )
        self.assertEqual(created["name"], "dashboards/d1")

    def test_create_dashboard_sends_an_empty_chart_list_verbatim(self):
        self.urlopen.return_value = _json_response({})

        self.client.create_dashboard({"title": "Run"}, dashboard_id="d1", charts=[])

        self.assertEqual(json.loads(self._sent_request().data)["charts"], [])

    def test_sends_the_bearer_token_and_json_content_type(self):
        self.urlopen.return_value = _json_response({})

        self.client.create_dashboard({}, dashboard_id="d1", charts=[])

        headers = self._sent_request().headers
        self.assertEqual(headers["Authorization"], f"Bearer {_TOKEN}")
        self.assertEqual(headers["Content-type"], "application/json")

    def test_http_error_surfaces_status_and_server_detail(self):
        self.urlopen.side_effect = _http_error(400, "charts[0].wid must be positive")

        with self.assertRaises(DashboardRestError) as raised:
            self.client.create_dashboard({}, dashboard_id="d1", charts=[])

        self.assertIn("HTTP 400", str(raised.exception))
        self.assertIn("charts[0].wid must be positive", str(raised.exception))

    def test_connection_failure_is_wrapped(self):
        self.urlopen.side_effect = urllib.error.URLError("name resolution failed")

        with self.assertRaises(DashboardRestError) as raised:
            self.client.create_dashboard({}, dashboard_id="d1", charts=[])

        self.assertIn("name resolution failed", str(raised.exception))


class ResourceIDFromNameTest(unittest.TestCase):
    """Covers extraction of trailing resource IDs from resource names."""

    def test_extracts_the_trailing_segment(self):
        self.assertEqual(
            resource_id_from_name("experiments/1/workUnits/2/charts/abc-123"), "abc-123"
        )

    def test_accepts_a_bare_resource_id(self):
        self.assertEqual(resource_id_from_name("abc-123"), "abc-123")

    def test_rejects_names_without_a_trailing_id(self):
        for name in ("", "charts/"):
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    resource_id_from_name(name)


if __name__ == "__main__":
    unittest.main()
