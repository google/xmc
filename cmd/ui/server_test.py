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
from unittest import mock

try:
  from cmd.ui import server
except ImportError:
  try:
    from xmanager_cloud.ui import server
  except ImportError:
    import server


class SPAHandlerTest(unittest.TestCase):

  def setUp(self):
    super().setUp()
    self.mock_wfile = io.BytesIO()
    self.mock_request = mock.create_autospec(
        socket.socket, instance=True, spec_set=True
    )
    self.mock_client_address = ('::1', 1234)
    self.mock_server = mock.create_autospec(
        http.server.HTTPServer, instance=True, spec_set=True
    )
    # Patch base class methods once per test to avoid side effects
    self.enter_context(mock.patch.object(
        server.SPAHandler, 'finish', autospec=True, spec_set=True
    ))
    self.enter_context(mock.patch.object(
        server.SPAHandler, 'handle', autospec=True, spec_set=True
    ))
    self.enter_context(mock.patch.object(
        server.SPAHandler, 'setup', autospec=True, spec_set=True
    ))

  def tearDown(self):
    self.mock_wfile.close()
    super().tearDown()

  def _get_handler(self, path):
    handler = server.SPAHandler(
        self.mock_request, self.mock_client_address, self.mock_server
    )
    handler.path = path
    handler.wfile = self.mock_wfile
    # Mocking base class methods to avoid side effects
    handler.send_response = mock.create_autospec(
        handler.send_response, spec_set=True
    )
    handler.send_header = mock.create_autospec(
        handler.send_header, spec_set=True
    )
    handler.end_headers = mock.create_autospec(
        handler.end_headers, spec_set=True
    )
    return handler

  def enter_context(self, context_manager):
    result = context_manager.__enter__()
    self.addCleanup(context_manager.__exit__, None, None, None)
    return result

  def _get_config_js_output(self, environ):
    handler = self._get_handler('/config.js')
    with mock.patch.dict(os.environ, environ, clear=True):
      handler.do_GET()

    handler.send_response.assert_called_with(200)
    handler.send_header.assert_any_call(
        'Content-type', 'application/javascript'
    )
    return self.mock_wfile.getvalue().decode()

  def test_healthz(self):
    handler = self._get_handler('/healthz')
    handler.do_GET()
    handler.send_response.assert_called_with(200, 'OK')
    handler.send_header.assert_any_call('Content-type', 'text/plain')

  def test_config_js_api_url(self):
    cases = [
        dict(
            testcase_name='defaults',
            environ={},
            expected='window.XMC_API_URL="";',
        ),
        dict(
            testcase_name='custom_env',
            environ={'XMC_API_URL': 'https://custom.api/v1'},
            expected='window.XMC_API_URL="https://custom.api/v1";',
        ),
    ]
    for case in cases:
      with self.subTest(case['testcase_name']):
        self.mock_wfile.seek(0)
        self.mock_wfile.truncate(0)
        output = self._get_config_js_output(case['environ'])
        self.assertIn(case['expected'], output)

  def test_config_js_refresh_interval(self):
    cases = [
        dict(
            testcase_name='defaults',
            environ={},
            expected='window.XMC_REFRESH_INTERVAL_MS=30000;',
        ),
        dict(
            testcase_name='custom_env',
            environ={'XMC_REFRESH_INTERVAL_MS': '60000'},
            expected='window.XMC_REFRESH_INTERVAL_MS=60000;',
        ),
        dict(
            testcase_name='invalid_interval',
            environ={'XMC_REFRESH_INTERVAL_MS': 'invalid'},
            expected='window.XMC_REFRESH_INTERVAL_MS=30000;',
        ),
    ]
    for case in cases:
      with self.subTest(case['testcase_name']):
        self.mock_wfile.seek(0)
        self.mock_wfile.truncate(0)
        output = self._get_config_js_output(case['environ'])
        self.assertIn(case['expected'], output)

  def test_config_js_feature_flags(self):
    cases = [
        dict(
            testcase_name='defaults',
            environ={},
            expected=(
                'window.XMC_FEATURE_FLAGS={'
                '"cursorPaginationEnabled": false, '
                '"statusMessageHistoryEnabled": false};'
            ),
        ),
        dict(
            testcase_name='cursor_pagination_true',
            environ={'XMC_DEFAULT_TO_CURSOR_PAGINATION': 'true'},
            expected=(
                'window.XMC_FEATURE_FLAGS={'
                '"cursorPaginationEnabled": true, '
                '"statusMessageHistoryEnabled": false};'
            ),
        ),
        dict(
            testcase_name='status_message_history_true',
            environ={'XMC_STATUS_MESSAGE_HISTORY_ENABLED': 'true'},
            expected=(
                'window.XMC_FEATURE_FLAGS={'
                '"cursorPaginationEnabled": false, '
                '"statusMessageHistoryEnabled": true};'
            ),
        ),
        dict(
            testcase_name='cursor_pagination_false',
            environ={'XMC_DEFAULT_TO_CURSOR_PAGINATION': 'false'},
            expected=(
                'window.XMC_FEATURE_FLAGS={'
                '"cursorPaginationEnabled": false, '
                '"statusMessageHistoryEnabled": false};'
            ),
        ),
        dict(
            testcase_name='cursor_pagination_other',
            environ={'XMC_DEFAULT_TO_CURSOR_PAGINATION': '1'},
            expected=(
                'window.XMC_FEATURE_FLAGS={'
                '"cursorPaginationEnabled": false, '
                '"statusMessageHistoryEnabled": false};'
            ),
        ),
    ]
    for case in cases:
      with self.subTest(case['testcase_name']):
        self.mock_wfile.seek(0)
        self.mock_wfile.truncate(0)
        output = self._get_config_js_output(case['environ'])
        self.assertIn(case['expected'], output)

  def test_fallback_to_index(self):
    self.enter_context(
        mock.patch.object(
            os.path, 'exists', return_value=False, autospec=True, spec_set=True
        )
    )
    self.enter_context(
        mock.patch.object(
            server.SPAHandler,
            'translate_path',
            return_value='/fake/path',
            autospec=True,
            spec_set=True,
        )
    )
    mock_do_get = self.enter_context(
        mock.patch.object(
            http.server.SimpleHTTPRequestHandler,
            'do_GET',
            autospec=True,
            spec_set=True,
        )
    )

    handler = self._get_handler('/some-route')
    handler.do_GET()

    self.assertEqual(handler.path, '/index.html')
    mock_do_get.assert_called_once_with(handler)

  def test_no_fallback_if_exists(self):
    self.enter_context(
        mock.patch.object(
            os.path, 'exists', return_value=True, autospec=True, spec_set=True
        )
    )
    self.enter_context(
        mock.patch.object(
            server.SPAHandler,
            'translate_path',
            return_value='/real/file.css',
            autospec=True,
            spec_set=True,
        )
    )
    mock_do_get = self.enter_context(
        mock.patch.object(
            http.server.SimpleHTTPRequestHandler,
            'do_GET',
            autospec=True,
            spec_set=True,
        )
    )

    handler = self._get_handler('/real/file.css')
    handler.do_GET()

    self.assertEqual(handler.path, '/real/file.css')
    mock_do_get.assert_called_once()


if __name__ == '__main__':
  unittest.main()
