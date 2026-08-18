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
"""Tests for server_local.py."""

import http.server
import io
import json
import os
import socket
import unittest
from typing import TYPE_CHECKING
from unittest import mock

if TYPE_CHECKING:
    from cmd.ui import server_local
else:
    try:
        from cmd.ui import server_local
    except ImportError:
        try:
            from xmanager_cloud.ui import server_local
        except ImportError:
            import server_local


class SPAHandlerLocalTest(unittest.TestCase):
    """Test suite for server_local.SPAHandler requests."""

    def setUp(self):
        super().setUp()
        self.mock_wfile = io.BytesIO()
        self.mock_request = mock.create_autospec(socket.socket, instance=True, spec_set=True)
        self.mock_client_address = ("::1", 1234)
        self.mock_server = mock.create_autospec(
            http.server.HTTPServer, instance=True, spec_set=True
        )
        self.enter_context(
            mock.patch.object(server_local.SPAHandler, "finish", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server_local.SPAHandler, "handle", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server_local.SPAHandler, "setup", autospec=True, spec_set=True)
        )
        server_local.SPAHandler.in_memory_dashboards = [
            {
                "name": "dashboards/vcc_benchmark_suite",
                "title": "VCC ResNet Benchmark Suite",
                "charts": [
                    {
                        "name": "experiments/234911122/charts/baseline",
                        "title": "VCC Baseline",
                        "xid": 234911122,
                        "wid": 1,
                    },
                    {
                        "name": "experiments/234911123/charts/tuned_v1",
                        "title": "VCC tuned_v1",
                        "xid": 234911123,
                        "wid": 1,
                    },
                    {
                        "name": "experiments/234911124/charts/tuned_v2",
                        "title": "VCC tuned_v2",
                        "xid": 234911124,
                        "wid": 1,
                    },
                ],
            }
        ]
        server_local.SPAHandler.goldens = {
            "experiments/234911122/charts/baseline": {
                "name": "experiments/234911122/charts/baseline",
                "xid": "234911122",
                "wid": "1",
                "plots_data": [
                    {"plot_id": "val_loss_kpi", "type": "SUMMARY_BOX"},
                    {"plot_id": "train_acc_kpi", "type": "SUMMARY_BOX"},
                    {"plot_id": "loss_vs_steps", "type": "SCATTER"},
                    {"plot_id": "step_time_dist", "type": "BAR"},
                ],
            },
            "experiments/9001/charts/scatter_multi": {
                "name": "experiments/9001/charts/scatter_multi",
                "xid": "9001",
                "plots_data": [
                    {"plot_id": "eval_loss_curve", "type": "SCATTER"},
                ],
            },
        }

    def tearDown(self):
        self.mock_wfile.close()
        super().tearDown()

    def _get_handler(self, path, method="GET", body=b""):
        handler = server_local.SPAHandler(
            self.mock_request, self.mock_client_address, self.mock_server
        )
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
        handler.send_header.assert_any_call("Content-Type", "application/javascript")
        return self.mock_wfile.getvalue().decode()

    def test_healthz(self):
        handler = self._get_handler("/healthz")
        handler.do_GET()
        handler.send_response.assert_called_with(200, "OK")
        handler.send_header.assert_any_call("Content-Type", "text/plain")

    def test_auth_user(self):
        handler = self._get_handler("/v1/auth/user")
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
        self.assertEqual(len(response_body["dashboards"]), 1)

    def test_dashboard_by_id(self):
        handler = self._get_handler("/v1/dashboards/vcc_benchmark_suite")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["name"], "dashboards/vcc_benchmark_suite")

    def test_dashboard_not_found(self):
        handler = self._get_handler("/v1/dashboards/non_existent")
        handler.do_GET()
        handler.send_response.assert_called_with(404)

    def test_create_dashboard(self):
        payload = json.dumps({"title": "Test Dash", "dashboardId": "test-dash"}).encode("utf-8")
        handler = self._get_handler("/v1/dashboards", method="POST", body=payload)
        handler.do_POST()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["name"], "dashboards/test-dash")
        self.assertEqual(response_body["title"], "Test Dash")
        self.assertEqual(len(server_local.SPAHandler.in_memory_dashboards), 2)

    def test_create_dashboard_invalid_json(self):
        handler = self._get_handler("/v1/dashboards", method="POST", body=b"invalid-json")
        handler.do_POST()
        handler.send_response.assert_called_with(400)

    def test_create_dashboard_non_dict_payload(self):
        handler = self._get_handler("/v1/dashboards", method="POST", body=b'"string-payload"')
        handler.do_POST()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["title"], "New Dashboard")

    def test_delete_dashboard(self):
        handler = self._get_handler("/v1/dashboards/vcc_benchmark_suite", method="DELETE")
        handler.do_DELETE()
        handler.send_response.assert_called_with(200)
        self.assertEqual(len(server_local.SPAHandler.in_memory_dashboards), 0)

        # Verify subsequent request sees the deletion
        self.mock_wfile.seek(0)
        self.mock_wfile.truncate(0)
        get_handler = self._get_handler("/v1/dashboards")
        get_handler.do_GET()
        get_handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["dashboards"], [])

    def test_delete_dashboard_not_found(self):
        handler = self._get_handler("/v1/dashboards/non_existent", method="DELETE")
        handler.do_DELETE()
        handler.send_response.assert_called_with(404)
        self.assertEqual(len(server_local.SPAHandler.in_memory_dashboards), 1)

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
        self.assertEqual(len(response_body["experiments"]), 4)
        xids = [e["xid"] for e in response_body["experiments"]]
        self.assertEqual(xids, [9001, 234911122, 234911123, 234911124])

    def test_single_experiment(self):
        handler = self._get_handler("/v1/experiments/234911122")
        handler.do_GET()
        handler.send_response.assert_called_with(200)
        response_body = json.loads(self.mock_wfile.getvalue().decode())
        self.assertEqual(response_body["xid"], 234911122)
        self.assertEqual(response_body["title"], "VCC Baseline")

    def test_get_experiments_fallback(self):
        original_dashboards = server_local.SPAHandler.in_memory_dashboards
        original_goldens = server_local.SPAHandler.goldens
        try:
            server_local.SPAHandler.in_memory_dashboards = []
            server_local.SPAHandler.goldens = {}
            experiments = server_local.SPAHandler.get_experiments()
            self.assertEqual(len(experiments), 4)
        finally:
            server_local.SPAHandler.in_memory_dashboards = original_dashboards
            server_local.SPAHandler.goldens = original_goldens

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

    def test_config_js_refresh_interval(self):
        cases = [
            {
                "testcase_name": "defaults",
                "environ": {},
                "expected": "window.XMC_REFRESH_INTERVAL_MS=30000;",
            },
            {
                "testcase_name": "custom_env",
                "environ": {"XMC_REFRESH_INTERVAL_MS": "60000"},
                "expected": "window.XMC_REFRESH_INTERVAL_MS=60000;",
            },
            {
                "testcase_name": "invalid_interval",
                "environ": {"XMC_REFRESH_INTERVAL_MS": "invalid"},
                "expected": "window.XMC_REFRESH_INTERVAL_MS=30000;",
            },
        ]
        for case in cases:
            with self.subTest(case["testcase_name"]):
                self.mock_wfile.seek(0)
                self.mock_wfile.truncate(0)
                output = self._get_config_js_output(case["environ"])
                self.assertIn(case["expected"], output)

    def test_config_js_feature_flags(self):
        cases = [
            {
                "testcase_name": "defaults",
                "environ": {},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"statusMessageHistoryEnabled": true,'
                    ' "adminPanelEnabled": false, "resourceDashboardEnabled": false,'
                    ' "performanceDashboardEnabled": false};'
                ),
            },
            {
                "testcase_name": "performance_dashboard_true",
                "environ": {"XMC_PERFORMANCE_DASHBOARD_ENABLED": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"statusMessageHistoryEnabled": true,'
                    ' "adminPanelEnabled": false, "resourceDashboardEnabled": false,'
                    ' "performanceDashboardEnabled": true};'
                ),
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
                server_local.SPAHandler,
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

        handler = self._get_handler("/some-route")
        handler.do_GET()

        self.assertEqual(handler.path, "/index.html")
        mock_do_get.assert_called_once_with(handler)

    def test_fallback_to_index_head(self):
        self.enter_context(
            mock.patch.object(os.path, "exists", return_value=False, autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(
                server_local.SPAHandler,
                "translate_path",
                return_value="/fake/path",
                autospec=True,
                spec_set=True,
            )
        )
        mock_do_head = self.enter_context(
            mock.patch.object(
                http.server.SimpleHTTPRequestHandler,
                "do_HEAD",
                autospec=True,
                spec_set=True,
            )
        )

        handler = self._get_handler("/some-route", method="HEAD")
        handler.do_HEAD()

        self.assertEqual(handler.path, "/index.html")
        mock_do_head.assert_called_once_with(handler)

    def test_no_fallback_if_exists(self):
        self.enter_context(
            mock.patch.object(os.path, "exists", return_value=True, autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(
                server_local.SPAHandler,
                "translate_path",
                return_value="/real/file.css",
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

        handler = self._get_handler("/real/file.css")
        handler.do_GET()

        self.assertEqual(handler.path, "/real/file.css")
        mock_do_get.assert_called_once()

    def test_initialize_mock_data_invalid_structure(self):
        mock_open_file = mock.mock_open(read_data=json.dumps({"error": "invalid structure"}))
        with (
            mock.patch.object(
                server_local,
                "find_testdata_dirs",
                return_value=["/fake/testdata"],
                autospec=True,
                spec_set=True,
            ),
            mock.patch.object(os.path, "exists", return_value=True, autospec=True, spec_set=True),
            mock.patch("builtins.open", mock_open_file),
        ):
            server_local.SPAHandler.initialize_mock_data()
            self.assertEqual(server_local.SPAHandler.in_memory_dashboards, [])


if __name__ == "__main__":
    unittest.main()
