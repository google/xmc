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

"""Unit tests for tools.nightly_tests.endpoint_resolution module."""

from __future__ import annotations

import unittest
from unittest import mock

from tools.nightly_tests import endpoint_resolution


class EndpointResolutionTest(unittest.TestCase):
    """Test cases for endpoint_resolution.py utilities."""

    def test_resolve_endpoint(self) -> None:
        # Provided endpoint takes precedence
        self.assertEqual(endpoint_resolution.resolve_endpoint("custom-ess:9090"), "custom-ess:9090")

        # Environment variable fallback
        with mock.patch.dict("os.environ", {"XMANAGER_ENDPOINT": "env-ess:8443"}, clear=True):
            self.assertEqual(endpoint_resolution.resolve_endpoint(None), "env-ess:8443")
            self.assertEqual(endpoint_resolution.resolve_endpoint(""), "env-ess:8443")

        # Default fallback when no env var is set
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(endpoint_resolution.resolve_endpoint(None), "localhost:8080")

    def test_resolve_dashboard_endpoint(self) -> None:
        # Provided endpoint takes highest precedence
        self.assertEqual(
            endpoint_resolution.resolve_dashboard_endpoint("https://custom-dash.example.com"),
            "https://custom-dash.example.com",
        )
        self.assertEqual(
            endpoint_resolution.resolve_dashboard_endpoint("http://custom-dash.example.com"),
            "http://custom-dash.example.com",
        )
        self.assertEqual(
            endpoint_resolution.resolve_dashboard_endpoint("localhost:8080"),
            "http://localhost:8080",
        )
        self.assertEqual(
            endpoint_resolution.resolve_dashboard_endpoint("127.0.0.1:9090"),
            "http://127.0.0.1:9090",
        )
        self.assertEqual(
            endpoint_resolution.resolve_dashboard_endpoint("dashboard.example.com"),
            "https://dashboard.example.com",
        )

        # Fallback to resolve_ui_base_url
        with mock.patch(
            "tools.nightly_tests.endpoint_resolution.resolve_ui_base_url",
            return_value="https://xmc-ui.example.com",
        ) as mock_ui:
            self.assertEqual(
                endpoint_resolution.resolve_dashboard_endpoint(None),
                "https://xmc-ui.example.com",
            )
            self.assertEqual(
                endpoint_resolution.resolve_dashboard_endpoint(""),
                "https://xmc-ui.example.com",
            )
            self.assertEqual(
                endpoint_resolution.resolve_dashboard_endpoint("   "),
                "https://xmc-ui.example.com",
            )
            self.assertEqual(mock_ui.call_count, 3)

    def test_resolve_clickhouse_endpoint(self) -> None:
        # Explicit parameter takes highest precedence
        self.assertEqual(
            endpoint_resolution.resolve_clickhouse_endpoint("http://clickhouse.internal:8123/"),
            "http://clickhouse.internal:8123",
        )
        self.assertEqual(
            endpoint_resolution.resolve_clickhouse_endpoint("clickhouse.internal:8123"),
            "http://clickhouse.internal:8123",
        )
        # Fallback to CLICKHOUSE_ENDPOINT
        with mock.patch.dict("os.environ", {"CLICKHOUSE_ENDPOINT": "ch-host:9000"}, clear=True):
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint(None), "http://ch-host:9000"
            )
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint("   "), "http://ch-host:9000"
            )
        # Fallback to CLICKHOUSE_HOST and CLICKHOUSE_HTTP_PORT
        with mock.patch.dict(
            "os.environ",
            {"CLICKHOUSE_HOST": "10.0.0.5", "CLICKHOUSE_HTTP_PORT": "8124"},
            clear=True,
        ):
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint(None), "http://10.0.0.5:8124"
            )
        # Fallback to CLICKHOUSE_HOST and CLICKHOUSE_PORT / default 8123
        with mock.patch.dict(
            "os.environ",
            {"CLICKHOUSE_HOST": "10.0.0.5", "CLICKHOUSE_PORT": "9009"},
            clear=True,
        ):
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint(None), "http://10.0.0.5:9009"
            )
        with mock.patch.dict("os.environ", {"CLICKHOUSE_HOST": "10.0.0.5"}, clear=True):
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint(None), "http://10.0.0.5:8123"
            )
        # Default fallback
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(
                endpoint_resolution.resolve_clickhouse_endpoint(None), "http://127.0.0.1:8123"
            )

    def test_resolve_ui_base_url(self) -> None:
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.resolve_ui_base_url",
            return_value="https://xmc-ui.example.com",
        ) as mock_resolve:
            result = endpoint_resolution.resolve_ui_base_url()
            self.assertEqual(result, "https://xmc-ui.example.com")
            mock_resolve.assert_called_once()


if __name__ == "__main__":
    unittest.main()
