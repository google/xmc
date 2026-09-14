# Copyright 2025 Google LLC
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
import os
import socket
import unittest
from typing import TYPE_CHECKING, Any
from unittest import mock

# TYPE_CHECKING is True only during static type analysis (e.g. Mypy/IDEs).
# This exposes the canonical static import 'from cmd.ui import server' to Mypy
# and Code Search tools without triggering duplicate symbol warnings (no-redef),
# while preserving runtime fallback imports for different execution environments
# (Bazel test runner, monorepo, and standalone execution).
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
        # Patch base class methods once per test to avoid side effects
        self.enter_context(
            mock.patch.object(server.SPAHandler, "finish", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server.SPAHandler, "handle", autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(server.SPAHandler, "setup", autospec=True, spec_set=True)
        )

    def tearDown(self):
        self.mock_wfile.close()
        super().tearDown()

    def _get_handler(self, path):
        handler = server.SPAHandler(self.mock_request, self.mock_client_address, self.mock_server)
        handler.path = path
        handler.wfile = self.mock_wfile
        # Mock instance methods to suppress side-effects during handler unit tests.
        # Mypy disallows method reassignment on instances, requiring explicit [method-assign] ignore.
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

    def test_config_js_api_url(self):
        cases = [
            {
                "testcase_name": "defaults",
                "environ": {},
                "expected": 'window.XMC_API_URL="https://api.example.com/v1";',
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
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "admin_panel_true",
                "environ": {"XMC_ADMIN_PANEL_ENABLED": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": true,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "admin_panel_false",
                "environ": {"XMC_ADMIN_PANEL_ENABLED": "false"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "resource_dashboard_true",
                "environ": {"XMC_RESOURCE_DASHBOARD_ENABLED": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": true, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "resource_dashboard_false",
                "environ": {"XMC_RESOURCE_DASHBOARD_ENABLED": "false"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "performance_dashboard_true",
                "environ": {"XMC_PERFORMANCE_DASHBOARD_ENABLED": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' true, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "performance_dashboard_false",
                "environ": {"XMC_PERFORMANCE_DASHBOARD_ENABLED": "false"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "non_prod_auth_true",
                "environ": {"XMC_NON_PROD_AUTH": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": true, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "non_prod_auth_numeric_1",
                "environ": {"XMC_NON_PROD_AUTH": "1"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": true, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "non_prod_auth_yes",
                "environ": {"XMC_NON_PROD_AUTH": "yes"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": true, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "show_safety_warning_true",
                "environ": {"SHOW_SAFETY_WARNING": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": true, "pauseSchedulingEnabled": false};'
                ),
            },
            {
                "testcase_name": "pause_scheduling_enabled_true",
                "environ": {"XMC_PAUSE_SCHEDULING_ENABLED": "true"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": true};'
                ),
            },
            {
                "testcase_name": "pause_scheduling_enabled_false",
                "environ": {"XMC_PAUSE_SCHEDULING_ENABLED": "false"},
                "expected": (
                    'window.XMC_FEATURE_FLAGS={"adminPanelEnabled": false,'
                    ' "resourceDashboardEnabled": false, "performanceDashboardEnabled":'
                    ' false, "isNonProdAuth": false, "pauseSchedulingEnabled": false};'
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

        handler = self._get_handler("/some-route")
        handler.do_GET()

        self.assertEqual(handler.path, "/index.html")
        mock_do_get.assert_called_once_with(handler)

    def test_no_fallback_if_exists(self):
        self.enter_context(
            mock.patch.object(os.path, "exists", return_value=True, autospec=True, spec_set=True)
        )
        self.enter_context(
            mock.patch.object(
                server.SPAHandler,
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

    def test_get_bool_env(self):
        cases: list[dict[str, Any]] = [
            {"env_val": "true", "default": False, "expected": True},
            {"env_val": "TRUE", "default": False, "expected": True},
            {"env_val": "false", "default": True, "expected": False},
            {"env_val": "FALSE", "default": True, "expected": False},
            {"env_val": None, "default": True, "expected": True},
            {"env_val": None, "default": False, "expected": False},
        ]
        for case in cases:
            env = {"TEST_VAR": case["env_val"]} if case["env_val"] is not None else {}
            with mock.patch.dict(os.environ, env, clear=True):
                self.assertEqual(
                    server.get_bool_env("TEST_VAR", default=case["default"]),
                    case["expected"],
                )

    def test_generate_runtime_config_defaults(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            config = server.generate_runtime_config()
            self.assertIn('window.XMC_API_URL="https://api.example.com/v1";', config)
            self.assertIn("window.XMC_REFRESH_INTERVAL_MS=30000;", config)

    def test_generate_runtime_config_custom(self):
        with mock.patch.dict(
            os.environ,
            {
                "XMC_API_URL": "https://custom.api/v1",
                "XMC_REFRESH_INTERVAL_MS": "15000",
                "XMC_PERFORMANCE_DASHBOARD_ENABLED": "true",
            },
            clear=True,
        ):
            config = server.generate_runtime_config(default_api_url="/fallback")
            self.assertIn('window.XMC_API_URL="https://custom.api/v1";', config)
            self.assertIn("window.XMC_REFRESH_INTERVAL_MS=15000;", config)
            self.assertIn('"performanceDashboardEnabled": true', config)

    def test_fallback_to_index_head(self):
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
        mock_do_head = self.enter_context(
            mock.patch.object(
                http.server.SimpleHTTPRequestHandler,
                "do_HEAD",
                autospec=True,
                spec_set=True,
            )
        )

        handler = self._get_handler("/some-route")
        handler.do_HEAD()

        self.assertEqual(handler.path, "/index.html")
        mock_do_head.assert_called_once_with(handler)

    def test_head_special_routes(self):
        handler_healthz = self._get_handler("/healthz")
        handler_healthz.do_HEAD()
        handler_healthz.send_response.assert_called_with(200, "OK")

        handler_config = self._get_handler("/config.js")
        handler_config.do_HEAD()
        handler_config.send_response.assert_called_with(200)


if __name__ == "__main__":
    unittest.main()
