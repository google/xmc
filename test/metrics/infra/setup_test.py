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

"""Unit tests for Python metrics testing harness (test/metrics/infra/setup.py)."""

import os
import sys
import unittest
from unittest.mock import MagicMock, patch

from test.metrics.infra.setup import (
    Endpoint,
    MetricsTestHarness,
    create_test_harness,
    parse_endpoint,
    resolve_container_endpoint,
    verify_tcp_connectivity,
)


class TestMetricsInfraSetup(unittest.TestCase):
    """Test suite for Python metrics harness infrastructure."""

    def test_endpoint_properties(self) -> None:
        """Verifies Endpoint address formatting."""
        ep = Endpoint(host="127.0.0.1", port=9092)
        self.assertEqual(ep.address, "127.0.0.1:9092")

    def test_parse_endpoint(self) -> None:
        """Verifies parsing string, tuple, and Endpoint instances."""
        ep1 = parse_endpoint("192.168.1.1:9000", default_port=80)
        self.assertEqual(ep1, Endpoint("192.168.1.1", 9000))

        ep2 = parse_endpoint("10.0.0.1", default_port=8123)
        self.assertEqual(ep2, Endpoint("10.0.0.1", 8123))

        ep3 = parse_endpoint(("10.0.0.1", 9092), default_port=80)
        self.assertEqual(ep3, Endpoint("10.0.0.1", 9092))

        ep_orig = Endpoint("127.0.0.1", 9092)
        self.assertEqual(parse_endpoint(ep_orig, default_port=80), ep_orig)

        for wildcard in ("0.0.0.0", "::", "[::]", "localhost"):
            ep_norm = parse_endpoint(f"{wildcard}:9092", default_port=9092)
            self.assertEqual(ep_norm, Endpoint("127.0.0.1", 9092))

    def test_resolve_container_endpoint_fallback(self) -> None:
        """Ensures fallback defaults are returned when Docker CLI fails or parses invalid text."""
        with patch("subprocess.run", side_effect=OSError("docker process error")):
            ep = resolve_container_endpoint(
                container_name="redpanda",
                internal_port=9092,
                default_host="127.0.0.1",
                default_port=9092,
            )
            self.assertEqual(ep, Endpoint("127.0.0.1", 9092))

        mock_bad_res = MagicMock()
        mock_bad_res.returncode = 0
        mock_bad_res.stdout = "invalid-stdout-without-delimiter\n"
        with patch("subprocess.run", return_value=mock_bad_res):
            ep_invalid = resolve_container_endpoint(
                container_name="redpanda",
                internal_port=9092,
                default_host="127.0.0.1",
                default_port=9092,
            )
            self.assertEqual(ep_invalid, Endpoint("127.0.0.1", 9092))

    def test_resolve_container_endpoint_mock_docker(self) -> None:
        """Verifies successful parsing of docker port CLI output and 0.0.0.0 normalization."""
        mock_res = MagicMock()
        mock_res.returncode = 0
        mock_res.stdout = "0.0.0.0:32768\n"

        with patch("subprocess.run", return_value=mock_res) as mock_run:
            ep = resolve_container_endpoint(
                container_name="my-container",
                internal_port=9092,
                default_host="127.0.0.1",
            )
            self.assertEqual(ep, Endpoint("127.0.0.1", 32768))
            mock_run.assert_called_once_with(
                ["docker", "port", "--", "my-container", "9092/tcp"],
                capture_output=True,
                text=True,
                check=False,
                timeout=3.0,
            )

    def test_env_overrides_extraction(self) -> None:
        """Verifies env_overrides extract endpoint attributes during initialization."""
        harness = MetricsTestHarness(
            env_overrides={
                "XMC_REDPANDA_BROKERS": "custom-broker:9092",
                "XMC_CLICKHOUSE_ADDR": "custom-ch:9000",
                "XMC_CLICKHOUSE_HTTP_ADDR": "custom-ch-http:8123",
                "XMC_GATEWAY_URL": "http://custom-gw:8080",
            }
        )
        self.assertEqual(harness.redpanda, Endpoint("custom-broker", 9092))
        self.assertEqual(harness.clickhouse_native, Endpoint("custom-ch", 9000))
        self.assertEqual(harness.clickhouse_http, Endpoint("custom-ch-http", 8123))
        self.assertEqual(harness.gateway_url, "http://custom-gw:8080")

    def test_harness_env_context(self) -> None:
        """Verifies dynamic application and restoration of environment variables."""
        harness = MetricsTestHarness(
            redpanda="127.0.0.1:9092",
            clickhouse_native="127.0.0.1:9000",
            clickhouse_http="127.0.0.1:8123",
            gateway_url="http://127.0.0.1:8080",
            env_overrides={"XMC_CUSTOM_FLAG": "true"},
        )

        test_key = "XMC_REDPANDA_BROKERS"
        self.assertNotIn(test_key, os.environ)

        with harness.env_context():
            self.assertEqual(os.environ.get("XMC_REDPANDA_BROKERS"), "127.0.0.1:9092")
            self.assertEqual(os.environ.get("XMC_CLICKHOUSE_ADDR"), "127.0.0.1:9000")
            self.assertEqual(os.environ.get("XMC_GATEWAY_URL"), "http://127.0.0.1:8080")
            self.assertEqual(os.environ.get("XMC_CUSTOM_FLAG"), "true")

        self.assertNotIn(test_key, os.environ)
        self.assertNotIn("XMC_CUSTOM_FLAG", os.environ)

    def test_tcp_connectivity_and_smoke_test(self) -> None:
        """Verifies TCP connectivity checks using mocked socket connections."""
        with patch("socket.create_connection", return_value=MagicMock()):
            self.assertTrue(verify_tcp_connectivity("127.0.0.1", 9092, timeout=2.0))

            harness = MetricsTestHarness(
                redpanda=("127.0.0.1", 9092),
                clickhouse_native=("127.0.0.1", 9000),
                clickhouse_http=("127.0.0.1", 8123),
            )
            smoke_res = harness.smoke_test(timeout=2.0)
            self.assertTrue(smoke_res["redpanda"])
            self.assertTrue(smoke_res["clickhouse_native"])
            self.assertTrue(smoke_res["clickhouse_http"])

            harness.verify_all_endpoints(timeout=2.0)

        with patch("socket.create_connection", side_effect=OSError("connection refused")):
            self.assertFalse(verify_tcp_connectivity("127.0.0.1", 9092, timeout=0.1))

    def test_verify_all_endpoints_failure_message(self) -> None:
        """Verifies detailed error message formatting when smoke test fails."""
        harness = MetricsTestHarness(
            redpanda="127.0.0.1:1",
            clickhouse_native="127.0.0.1:2",
            clickhouse_http="127.0.0.1:3",
        )
        with self.assertRaises(ConnectionError) as ctx:
            harness.verify_all_endpoints(timeout=0.1)

        err_msg = str(ctx.exception)
        self.assertIn("redpanda (127.0.0.1:1)", err_msg)
        self.assertIn("clickhouse_native (127.0.0.1:2)", err_msg)
        self.assertIn("clickhouse_http (127.0.0.1:3)", err_msg)

    def test_create_test_harness_factory(self) -> None:
        """Verifies factory creation of harness with auto-resolution disabled."""
        harness = create_test_harness(auto_resolve_containers=False)
        self.assertEqual(harness.redpanda.address, "127.0.0.1:9092")
        self.assertEqual(harness.clickhouse_native.address, "127.0.0.1:9000")

    def test_sys_modules_purity(self) -> None:
        """Ensures importing setup module leaks zero forbidden third-party libraries."""
        forbidden = {"requests", "httpx", "aiohttp", "urllib3"}
        loaded = {mod.split(".")[0] for mod in sys.modules}
        leaked = forbidden.intersection(loaded)
        self.assertFalse(leaked, f"Forbidden third-party libraries leaked: {leaked}")


if __name__ == "__main__":
    unittest.main()
