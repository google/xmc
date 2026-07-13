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
"""A simple Python server for serving the UI and handling fallback routing."""

import http.server
import json
import os

class SPAHandler(http.server.SimpleHTTPRequestHandler):
  """Custom request handler that supports SPA routing fallbacks and config.js."""

  def do_GET(self):
    if self.path == '/healthz':
      self.send_response(200, 'OK')
      self.send_header('Content-type', 'text/plain')
      self.end_headers()
      self.wfile.write(b'OK')
      return

    if self.path == '/config.js':
      self.send_response(200)
      self.send_header('Content-type', 'application/javascript')
      self.end_headers()

      api_url = os.environ.get('XMC_API_URL')
      if not api_url:
        self.log_message("WARNING: XMC_API_URL environment variable is not set. Defaulting to 'https://api.example.com/v1'")
        api_url = 'https://api.example.com/v1'

      refresh_interval_str = os.environ.get('XMC_REFRESH_INTERVAL_MS', '30000')
      try:
        refresh_interval = int(refresh_interval_str)
      except ValueError:
        refresh_interval = 30000

      status_history = os.environ.get('XMC_STATUS_MESSAGE_HISTORY_ENABLED', 'false').lower() == 'true'

      feature_flags = {
          'statusMessageHistoryEnabled': status_history,
      }

      config_content = (
          f'window.XMC_API_URL="{api_url}";\n'
          f'window.XMC_REFRESH_INTERVAL_MS={refresh_interval};\n'
          f'window.XMC_FEATURE_FLAGS={json.dumps(feature_flags)};\n'
      )
      self.wfile.write(config_content.encode('utf-8'))
      return

    translated_path = self.translate_path(self.path)
    if not os.path.exists(translated_path):
      self.path = '/index.html'

    super().do_GET()
