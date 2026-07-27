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

"""Unit tests for XMC zero-dependency HTTP client and connection recycling."""

import threading
import unittest
from unittest import mock

from xmc.network import HTTPClient, SendResult


class TestSendResultEnum(unittest.TestCase):
    """Tests for SendResult enum values."""

    def test_send_result_enum_members(self) -> None:
        self.assertEqual(SendResult.SUCCESS.value, "SUCCESS")
        self.assertEqual(SendResult.INVALID_PAYLOAD.value, "INVALID_PAYLOAD")
        self.assertEqual(SendResult.TRANSIENT_FAILURE.value, "TRANSIENT_FAILURE")


class TestHTTPClient(unittest.TestCase):
    """Tests for HTTPClient connection management and request sending."""

    def setUp(self) -> None:
        import os

        self._orig_env = os.environ.copy()
        os.environ["XMC_XID"] = "1001"
        os.environ["XMC_WORK_UNIT_ID"] = "2002"
        os.environ["XMC_EXECUTION_ID"] = "3003"

    def tearDown(self) -> None:
        import os

        os.environ.clear()
        os.environ.update(self._orig_env)

    def test_invalid_endpoint_raises_value_error(self) -> None:
        with self.assertRaises(ValueError):
            HTTPClient(endpoint="")
        with self.assertRaises(ValueError):
            HTTPClient(endpoint="ftp://localhost:8080")

    def test_default_timeout_is_12_seconds(self) -> None:
        client = HTTPClient("http://localhost:8080")
        self.assertEqual(client.timeout, 12.0)

    @mock.patch("http.client.HTTPConnection")
    def test_keep_alive_headers_and_successful_send(
        self, mock_http_conn_cls: mock.MagicMock
    ) -> None:
        mock_conn = mock.MagicMock()
        mock_response = mock.MagicMock()
        mock_response.status = 200
        mock_conn.getresponse.return_value = mock_response
        mock_http_conn_cls.return_value = mock_conn

        client = HTTPClient("http://example.com:8080")
        result = client.send(
            body='{"metric_name": "loss", "metric_value": 0.5}',
            path="/v1/metrics",
        )

        self.assertEqual(result, SendResult.SUCCESS)
        mock_http_conn_cls.assert_called_once_with("example.com", port=8080, timeout=12.0)
        mock_conn.request.assert_called_once()
        _, kwargs = mock_conn.request.call_args
        headers = kwargs.get("headers", {})
        self.assertEqual(headers.get("Connection"), "keep-alive")
        self.assertEqual(headers.get("Content-Type"), "application/json")

    @mock.patch("http.client.HTTPSConnection")
    def test_https_connection_created(self, mock_https_conn_cls: mock.MagicMock) -> None:
        mock_conn = mock.MagicMock()
        mock_response = mock.MagicMock()
        mock_response.status = 200
        mock_conn.getresponse.return_value = mock_response
        mock_https_conn_cls.return_value = mock_conn

        client = HTTPClient("https://metrics.xmc.dev")
        result = client.send("test", path="/v1/log")

        self.assertEqual(result, SendResult.SUCCESS)
        mock_https_conn_cls.assert_called_once_with("metrics.xmc.dev", port=None, timeout=12.0)

    @mock.patch("http.client.HTTPConnection")
    def test_status_code_mappings(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn = mock.MagicMock()
        mock_response = mock.MagicMock()
        mock_conn.getresponse.return_value = mock_response
        mock_http_conn_cls.return_value = mock_conn

        client = HTTPClient("http://localhost:8080")

        # 2xx -> SUCCESS
        mock_response.status = 204
        self.assertEqual(client.send("data"), SendResult.SUCCESS)

        # 4xx -> INVALID_PAYLOAD
        mock_response.status = 400
        self.assertEqual(client.send("data"), SendResult.INVALID_PAYLOAD)

        mock_response.status = 422
        self.assertEqual(client.send("data"), SendResult.INVALID_PAYLOAD)

        # 5xx -> TRANSIENT_FAILURE
        mock_response.status = 500
        self.assertEqual(client.send("data"), SendResult.TRANSIENT_FAILURE)

        mock_response.status = 503
        self.assertEqual(client.send("data"), SendResult.TRANSIENT_FAILURE)

    @mock.patch("http.client.HTTPConnection")
    def test_connection_recycling_by_max_requests(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn1 = mock.MagicMock()
        mock_conn2 = mock.MagicMock()
        mock_resp = mock.MagicMock()
        mock_resp.status = 200
        mock_conn1.getresponse.return_value = mock_resp
        mock_conn2.getresponse.return_value = mock_resp
        mock_http_conn_cls.side_effect = [mock_conn1, mock_conn2]

        client = HTTPClient("http://localhost:8080", max_requests=2)

        # First request -> connection 1
        res1 = client.send("body1")
        self.assertEqual(res1, SendResult.SUCCESS)

        # Second request -> connection 1 reused
        res2 = client.send("body2")
        self.assertEqual(res2, SendResult.SUCCESS)

        # Third request -> max_requests exceeded, recycled -> connection 2 created
        res3 = client.send("body3")
        self.assertEqual(res3, SendResult.SUCCESS)

        self.assertEqual(mock_http_conn_cls.call_count, 2)
        mock_conn1.close.assert_called_once()

    @mock.patch("time.monotonic")
    @mock.patch("http.client.HTTPConnection")
    def test_connection_recycling_by_ttl_and_jitter(
        self, mock_http_conn_cls: mock.MagicMock, mock_monotonic: mock.MagicMock
    ) -> None:
        mock_conn1 = mock.MagicMock()
        mock_conn2 = mock.MagicMock()
        mock_resp = mock.MagicMock()
        mock_resp.status = 200
        mock_conn1.getresponse.return_value = mock_resp
        mock_conn2.getresponse.return_value = mock_resp
        mock_http_conn_cls.side_effect = [mock_conn1, mock_conn2]

        # Time sequence: t0 (creation), t1 (< TTL), t2 (>= TTL), t3 (re-creation)
        mock_monotonic.side_effect = [100.0, 200.0, 800.0, 800.0]

        client = HTTPClient("http://localhost:8080", base_ttl=600.0, jitter=0.0)

        # Request 1 at t=100s
        client.send("body1")
        self.assertEqual(mock_http_conn_cls.call_count, 1)

        # Request 2 at t=200s (age = 100s < 600s TTL) -> reused
        client.send("body2")
        self.assertEqual(mock_http_conn_cls.call_count, 1)

        # Request 3 at t=800s (age = 700s >= 600s TTL) -> recycled
        client.send("body3")
        self.assertEqual(mock_http_conn_cls.call_count, 2)
        mock_conn1.close.assert_called_once()

    @mock.patch("http.client.HTTPConnection")
    def test_stale_connection_retry_success(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn1 = mock.MagicMock()
        mock_conn2 = mock.MagicMock()
        mock_resp = mock.MagicMock()
        mock_resp.status = 200

        # Connection 1 fails on request due to stale socket error
        mock_conn1.request.side_effect = OSError("Connection reset by peer")
        mock_conn2.getresponse.return_value = mock_resp
        mock_http_conn_cls.side_effect = [mock_conn1, mock_conn2]

        client = HTTPClient("http://localhost:8080")
        result = client.send("body")

        self.assertEqual(result, SendResult.SUCCESS)
        self.assertEqual(mock_http_conn_cls.call_count, 2)
        mock_conn1.close.assert_called_once()

    @mock.patch("http.client.HTTPConnection")
    def test_thread_safe_concurrent_sends(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn = mock.MagicMock()
        mock_resp = mock.MagicMock()
        mock_resp.status = 200
        mock_conn.getresponse.return_value = mock_resp
        mock_http_conn_cls.return_value = mock_conn

        client = HTTPClient("http://localhost:8080")
        results = []

        def worker() -> None:
            res = client.send("body")
            results.append(res)

        threads = [threading.Thread(target=worker) for _ in range(10)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(len(results), 10)
        self.assertTrue(all(r == SendResult.SUCCESS for r in results))

    @mock.patch("http.client.HTTPConnection")
    def test_socket_timeout_recycles_connection(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn = mock.MagicMock()
        mock_conn.request.side_effect = TimeoutError("timed out")
        mock_http_conn_cls.return_value = mock_conn

        client = HTTPClient("http://localhost:8080")
        result = client.send("body")

        self.assertEqual(result, SendResult.TRANSIENT_FAILURE)
        mock_conn.close.assert_called_once()

    @mock.patch("http.client.HTTPConnection")
    def test_context_manager_closes_connection(self, mock_http_conn_cls: mock.MagicMock) -> None:
        mock_conn = mock.MagicMock()
        mock_resp = mock.MagicMock()
        mock_resp.status = 200
        mock_conn.getresponse.return_value = mock_resp
        mock_http_conn_cls.return_value = mock_conn

        with HTTPClient("http://localhost:8080") as client:
            client.send("body")

        mock_conn.close.assert_called_once()

    def test_network_metadata(self) -> None:
        env = {
            "XMC_XID": "10",
            "XMC_WORK_UNIT_ID": "20",
            "XMC_EXECUTION_ID": "30",
            "XMC_REPLICA_ID": "replica-net",
        }
        client = HTTPClient(
            "http://localhost:8080",
            env=env,
        )
        self.assertEqual(client.xid, "10")
        self.assertEqual(client.work_unit_id, "20")
        self.assertEqual(client.execution_id, "30")
        self.assertEqual(client.replica_id, "replica-net")


if __name__ == "__main__":
    unittest.main()
