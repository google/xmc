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

"""Unit tests for XMC SDK base HTTP client."""

import io
import json
import os
import socket
import unittest
import urllib.error
from unittest.mock import MagicMock, patch

from xmc.client import Client, XMCAPIError, XMCError, XMCNetworkError


class TestClient(unittest.TestCase):
    """Unit tests for Client class and exceptions."""

    def test_init_invalid_base_url(self) -> None:
        with self.assertRaises(ValueError):
            Client(base_url="")

    def test_init_headers(self) -> None:
        client = Client(
            base_url="https://xmc.googleapis.com",
            api_key="test-token",
            default_headers={"X-Custom": "val"},
        )
        self.assertEqual(client.base_url, "https://xmc.googleapis.com")
        self.assertEqual(client._headers["Authorization"], "Bearer test-token")
        self.assertEqual(client._headers["X-Custom"], "val")
        self.assertEqual(client._headers["Content-Type"], "application/json")

    @patch("urllib.request.urlopen")
    def test_get_request_success(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"status": "ok"}).encode("utf-8")
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.googleapis.com")
        res = client.get("v1/metrics", params={"step": 1})

        self.assertEqual(res, {"status": "ok"})
        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_full_url(), "https://xmc.googleapis.com/v1/metrics?step=1")
        self.assertEqual(req.get_method(), "GET")

    @patch("urllib.request.urlopen")
    def test_post_request_success(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"id": "123"}).encode("utf-8")
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.googleapis.com")
        res = client.post("v1/metrics", data={"metric_name": "loss", "metric_value": 0.5})

        self.assertEqual(res, {"id": "123"})
        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_full_url(), "https://xmc.googleapis.com/v1/metrics")
        self.assertEqual(req.get_method(), "POST")

    @patch("urllib.request.urlopen")
    def test_empty_response(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = b""
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.googleapis.com")
        res = client.get("v1/empty")
        self.assertEqual(res, {})

    @patch("urllib.request.urlopen")
    def test_http_error(self, mock_urlopen: MagicMock) -> None:
        err_fp = io.BytesIO(b'{"error": "not found"}')
        http_err = urllib.error.HTTPError(
            url="https://xmc.googleapis.com/v1/missing",
            code=404,
            msg="Not Found",
            hdrs={},
            fp=err_fp,
        )
        mock_urlopen.side_effect = http_err

        client = Client(base_url="https://xmc.googleapis.com")
        with self.assertRaises(XMCAPIError) as ctx:
            client.get("v1/missing")

        self.assertEqual(ctx.exception.status_code, 404)
        self.assertIn("Not Found", str(ctx.exception))
        self.assertEqual(ctx.exception.response_body, '{"error": "not found"}')

    @patch("urllib.request.urlopen")
    def test_url_error_timeout(self, mock_urlopen: MagicMock) -> None:
        url_err = urllib.error.URLError(reason=socket.timeout("connection timed out"))
        mock_urlopen.side_effect = url_err

        client = Client(base_url="https://xmc.googleapis.com")
        with self.assertRaises(XMCNetworkError) as ctx:
            client.get("v1/metrics")

        self.assertIn("Request timed out", str(ctx.exception))

    @patch("urllib.request.urlopen")
    def test_json_decode_error(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = b"invalid json"
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.googleapis.com")
        with self.assertRaises(XMCError) as ctx:
            client.get("v1/metrics")

        self.assertIn("Failed to parse API JSON response", str(ctx.exception))

    @patch("urllib.request.urlopen")
    def test_get_request_sequence_params(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps([{"id": 1}, {"id": 2}]).encode("utf-8")
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        client = Client(base_url="https://xmc.googleapis.com")
        res = client.get("v1/metrics", params={"tag": ["a", "b"]})

        self.assertEqual(res, [{"id": 1}, {"id": 2}])
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_full_url(), "https://xmc.googleapis.com/v1/metrics?tag=a&tag=b")

    @patch("urllib.request.urlopen")
    def test_timeout_error_direct(self, mock_urlopen: MagicMock) -> None:
        mock_urlopen.side_effect = TimeoutError("request timeout")

        client = Client(base_url="https://xmc.googleapis.com")
        with self.assertRaises(XMCNetworkError) as ctx:
            client.get("v1/metrics")

        self.assertIn("Request timed out", str(ctx.exception))

    def test_context_manager(self) -> None:
        with Client(base_url="https://xmc.googleapis.com") as client:
            self.assertEqual(client.base_url, "https://xmc.googleapis.com")


class TestClientShortCircuit(unittest.TestCase):
    """Unit tests for Client short-circuit / disabling feature."""

    def test_default_disabled_is_false(self) -> None:
        with patch.dict("os.environ"):
            os.environ.pop("XMC_METRICS_DISABLED", None)
            client = Client(base_url="https://xmc.googleapis.com")
            self.assertFalse(client.disabled)

    def test_env_var_enabled_truthy_values(self) -> None:
        truthy_values = ["1", "true", "TRUE", "yes", "YES", "t", "T", "y", "Y", "  true  "]
        for val in truthy_values:
            with patch.dict("os.environ", {"XMC_METRICS_DISABLED": val}):
                client = Client(base_url="https://xmc.googleapis.com")
                self.assertTrue(client.disabled, f"Failed for env val: {val!r}")

    def test_env_var_falsy_values(self) -> None:
        falsy_values = ["0", "false", "no", "n", "random", ""]
        for val in falsy_values:
            with patch.dict("os.environ", {"XMC_METRICS_DISABLED": val}):
                client = Client(base_url="https://xmc.googleapis.com")
                self.assertFalse(client.disabled, f"Failed for env val: {val!r}")

    def test_explicit_disabled_true_overrides_env(self) -> None:
        with patch.dict("os.environ", {"XMC_METRICS_DISABLED": "0"}):
            client = Client(base_url="https://xmc.googleapis.com", disabled=True)
            self.assertTrue(client.disabled)

    def test_explicit_disabled_false_overrides_env(self) -> None:
        with patch.dict("os.environ", {"XMC_METRICS_DISABLED": "1"}):
            client = Client(base_url="https://xmc.googleapis.com", disabled=False)
            self.assertFalse(client.disabled)

    @patch("urllib.request.urlopen")
    def test_short_circuit_returns_empty_dict_no_urlopen(self, mock_urlopen: MagicMock) -> None:
        client = Client(base_url="https://xmc.googleapis.com", disabled=True)

        res_get = client.get("v1/metrics", params={"step": 1})
        self.assertEqual(res_get, {})

        res_post = client.post("v1/metrics", data={"metric_name": "loss", "metric_value": 0.5})
        self.assertEqual(res_post, {})

        res_req = client.request("DELETE", "v1/metrics/123")
        self.assertEqual(res_req, {})

        mock_urlopen.assert_not_called()

    def test_disabled_property_read_only(self) -> None:
        client = Client(base_url="https://xmc.googleapis.com", disabled=True)
        with self.assertRaises(AttributeError):
            client.disabled = False


if __name__ == "__main__":
    unittest.main()
