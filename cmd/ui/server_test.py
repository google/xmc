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
"""Tests for server.py."""

import http.server
import io
import json
import os
import socket
import unittest
from typing import TYPE_CHECKING
from unittest import mock

if TYPE_CHECKING:
    from cmd.ui import server
else:
    try:
        from cmd.ui import server
    except ImportError:
        try:
            from xmanager_cloud.ui import server
        except ImportError:
            import server


class SPAHandlerTest(unittest.TestCase):
    """Test suite for SPAHandler server requests."""

    def setUp(self):
        super().setUp()
        self.mock_wfile = io.BytesIO()
        self.mock_request = mock.create_autospec(socket.socket, instance=True, spec_set=True)
        self.mock_client_address = ("::1", 1234)
        self.mock_server = mock.create_autospec(
            http.server.HTTPServer, instance=True, spec_set=True
        )
        self.enter_context(
            mock.patch.object(server.SPAHandler, "finish", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server.SPAHandler, "handle", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server.SPAHandler, "setup", autospec=True, spec_set=True)
        )
        server.SPAHandler.initialize_mock_data()

    def tearDown(self):
        self.mock_wfile.close()
        super().tearDown()

    def _get_handler(self, path, method="GET", body=b""):
        handler = server.SPAHandler(self.mock_request, self.mock_client_address, self.mock_server)
        handler.path = path
        handler.command = method
        handler.wfile = self.mock_wfile
        handler.rfile = io.BytesIO(body)
        handler.headers = {"Content-Length": str(len(body))}
        handler.send_response = mock.create_autospec(handler.send_response, spec_set=True)  # type: ignore[method-assign]
        handler.send_header = mock.create_autospec(handler.send_header, spec_set=True)  # type: ignore[method-assign]
        handler.end_headers = mock.create_autospec(handler.end_headers, spec_set=True)  # type: ignore[method-assign]
        return handler

    def enter_context(self, context_manager):
        result = context_manager.__enter__()
        self.addCleanup(context_manager.__exit__, None, None, None)
        return result

    def _get_config_js_output(self, environ):
        handler = self._get_handler("/config.js")
        with mock.patch.dict(os.environ, environ, clear=True):
            handler.do_GET()

        handler.send_response.assert_called_with(200)
        handler.send_header.assert_any_call("Content-type", "application/javascript")
        return self.mock_wfile.getvalue().decode()

    def test_healthz(self):
        handler = self._get_handler("/healthz")
        handler.do_GET()
        handler.send_response.assert_called_with(200, "OK")
        handler.send_header.assert_any_call("Content-type", "text/plain")

    def test_mock_user(self):
        handler = self._get_handler("/api/user")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["email"], "developer@google.com")
        self.assertEqual(response_body["name"], "Local Developer")

    def test_dashboards_list(self):
        handler = self._get_handler("/v1/dashboards")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertIn("dashboards", response_body)

    def test_chart_data_golden(self):
        handler = self._get_handler("/v1/experiments/234911122/charts/baseline:getData")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["name"], "experiments/234911122/charts/baseline")
        self.assertIn("plots_data", response_body)
        self.assertEqual(len(response_body["plots_data"]), 4)

    def test_chart_data_golden_9001(self):
        handler = self._get_handler("/v1/experiments/9001/charts/scatter_multi:getData")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["name"], "experiments/9001/charts/scatter_multi")
        self.assertIn("plots_data", response_body)

    def test_experiments_list(self):
        handler = self._get_handler("/v1/experiments")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertIn("experiments", response_body)
        self.assertGreaterEqual(len(response_body["experiments"]), 3)

    def test_options_cors(self):
        handler = self._get_handler("/v1/dashboards", method="OPTIONS")
        handler.do_OPTIONS()
        handler.send_response.assert_called_with(204)
        handler.send_header.assert_any_call("Access-Control-Allow-Origin", "*")

    def test_config_js_api_url(self):
        cases = [
            {
                "testcase_name": "defaults",
                "environ": {},
                "expected": 'window.XMC_API_URL="/v1";',
            },
            {
                "testcase_name": "custom_env",
                "environ": {"XMC_API_URL": "https://custom.api/v1"},
                "expected": 'window.XMC_API_URL="https://custom.api/v1";',
            },
        ]
        for case in cases:
            with self.subTest(case["testcase_name"]):
                self.mock_wfile.seek(0)
                self.mock_wfile.truncate(0)
                output = self._get_config_js_output(case["environ"])
                self.assertIn(case["expected"], output)

    def test_fallback_to_index(self):
        self.enter_context(
            mock.patch.object(os.path, "exists", return_value=False, autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(
                server.SPAHandler,
                "translate_path",
                return_value="/fake/path",
                autospec=True,
                spec_set=True,
            )
        )
        mock_do_get = self.enter_context(
            mock.patch.object(
                http.server.SimpleHTTPRequestHandler,
                "do_GET",
                autospec=True,
                spec_set=True,
            )
        )

        handler = self._get_handler("/dashboards")
        handler.do_GET()

        mock_do_get.assert_called_once_with(handler)


if __name__ == "__main__":
    unittest.main()
