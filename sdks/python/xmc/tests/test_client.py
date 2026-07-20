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

import io
import json
import socket
import unittest
from unittest.mock import MagicMock, patch
import urllib.error

from xmc.client import Client, XMCAPIError, XMCError, XMCNetworkError


class TestClient(unittest.TestCase):
    """Unit tests for XMC HTTP Client."""

    def test_init_validation(self) -> None:
        with self.assertRaises(ValueError):
            Client(base_url="")

    def test_init_headers_and_auth(self) -> None:
        client = Client(
            base_url="https://xmc.example.com/",
            api_key="secret-token",
            default_headers={"X-Custom-Header": "value"},
        )
        self.assertEqual(client.base_url, "https://xmc.example.com")
        self.assertEqual(client.api_key, "secret-token")
        self.assertEqual(client._headers["Authorization"], "Bearer secret-token")
        self.assertEqual(client._headers["X-Custom-Header"], "value")
        self.assertEqual(client._headers["Content-Type"], "application/json")

    @patch("urllib.request.urlopen")
    def test_get_request_success(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"status": "ok"}).encode("utf-8")
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.example.com")
        res = client.get("/v1/metrics", params={"filter": "cpu"})

        self.assertEqual(res, {"status": "ok"})
        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(
            req.get_full_url(), "https://xmc.example.com/v1/metrics?filter=cpu"
        )
        self.assertEqual(req.get_method(), "GET")

    @patch("urllib.request.urlopen")
    def test_post_request_success(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"id": 123}).encode("utf-8")
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.example.com")
        payload = {"metric_name": "latency", "value": 42}
        res = client.post("v1/metrics", data=payload)

        self.assertEqual(res, {"id": 123})
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_full_url(), "https://xmc.example.com/v1/metrics")
        self.assertEqual(req.get_method(), "POST")
        self.assertEqual(req.data, json.dumps(payload).encode("utf-8"))

    @patch("urllib.request.urlopen")
    def test_empty_response(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = b""
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.example.com")
        res = client.request("DELETE", "/v1/metrics/123")
        self.assertEqual(res, {})

    @patch("urllib.request.urlopen")
    def test_http_error_handling(self, mock_urlopen: MagicMock) -> None:
        err_fp = io.BytesIO(b'{"error": "not found"}')
        http_err = urllib.error.HTTPError(
            url="https://xmc.example.com/v1/metrics",
            code=404,
            msg="Not Found",
            hdrs={},
            fp=err_fp,
        )
        mock_urlopen.side_effect = http_err

        client = Client(base_url="https://xmc.example.com")
        with self.assertRaises(XMCAPIError) as ctx:
            client.get("/v1/metrics")

        self.assertEqual(ctx.exception.status_code, 404)
        self.assertIn("Not Found", str(ctx.exception))
        self.assertEqual(ctx.exception.response_body, '{"error": "not found"}')

    @patch("urllib.request.urlopen")
    def test_url_error_handling(self, mock_urlopen: MagicMock) -> None:
        url_err = urllib.error.URLError(reason="Connection refused")
        mock_urlopen.side_effect = url_err

        client = Client(base_url="https://xmc.example.com")
        with self.assertRaises(XMCNetworkError) as ctx:
            client.get("/v1/metrics")

        self.assertIn("Connection refused", str(ctx.exception))

    @patch("urllib.request.urlopen")
    def test_timeout_error_handling(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = socket.timeout("timed out")

        client = Client(base_url="https://xmc.example.com")
        with self.assertRaises(XMCNetworkError) as ctx:
            client.get("/v1/metrics")

        self.assertIn("timed out", str(ctx.exception))

    @patch("urllib.request.urlopen")
    def test_invalid_json_handling(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = b"NOT VALID JSON"
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.example.com")
        with self.assertRaises(XMCError) as ctx:
            client.get("/v1/metrics")

        self.assertIn("Failed to parse API JSON response", str(ctx.exception))

    def test_context_manager(self) -> None:
        with Client(base_url="https://xmc.example.com") as client:
            self.assertEqual(client.base_url, "https://xmc.example.com")


if __name__ == "__main__":
    unittest.main()
