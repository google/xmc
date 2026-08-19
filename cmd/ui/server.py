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


def get_bool_env(name: str, default: bool = False) -> bool:
    """Reads a boolean environment variable, returning default if unset."""
    val = os.environ.get(name)
    if val is None:
        return default
    return val.lower() in ("true", "1", "yes")


def generate_runtime_config(
    default_api_url: str = "https://api.example.com/v1",
    api_url: str | None = None,
) -> str:
    """Generates dynamic runtime config.js matching deployment settings."""
    resolved_api_url = os.environ.get("XMC_API_URL", api_url or default_api_url)
    refresh_interval_str = os.environ.get("XMC_REFRESH_INTERVAL_MS", "30000")
    try:
        refresh_interval = int(refresh_interval_str)
    except ValueError:
        refresh_interval = 30000

    feature_flags = {
        "statusMessageHistoryEnabled": get_bool_env("XMC_STATUS_MESSAGE_HISTORY_ENABLED", True),
        "adminPanelEnabled": get_bool_env("XMC_ADMIN_PANEL_ENABLED", False),
        "resourceDashboardEnabled": get_bool_env("XMC_RESOURCE_DASHBOARD_ENABLED", False),
        "performanceDashboardEnabled": get_bool_env("XMC_PERFORMANCE_DASHBOARD_ENABLED", False),
        "isNonProdAuth": get_bool_env("XMC_NON_PROD_AUTH", False)
        or get_bool_env("SHOW_SAFETY_WARNING", False),
    }

    return (
        f'window.XMC_API_URL="{resolved_api_url}";\n'
        f"window.XMC_REFRESH_INTERVAL_MS={refresh_interval};\n"
        f"window.XMC_FEATURE_FLAGS={json.dumps(feature_flags)};\n"
    )


class SPAHandler(http.server.SimpleHTTPRequestHandler):
    """Custom request handler that supports SPA routing fallbacks and config.js."""

    def _apply_spa_fallback(self) -> None:
        """Translates path and rewrites to index.html if file is missing."""
        translated_path = self.translate_path(self.path)
        if not os.path.exists(translated_path):
            self.path = "/index.html"

    def _handle_config_js(self) -> None:
        """Handles config.js GET requests."""
        self.send_response(200)
        self.send_header("Content-type", "application/javascript")
        self.end_headers()

        if not os.environ.get("XMC_API_URL"):
            self.log_message(
                "WARNING: XMC_API_URL environment variable is not set. Defaulting"
                " to 'https://api.example.com/v1'"
            )

        config_content = generate_runtime_config(default_api_url="https://api.example.com/v1")
        self.wfile.write(config_content.encode("utf-8"))

    def do_GET(self) -> None:
        """Handles GET requests for SPA routing, health checks, and config."""
        if self.path == "/healthz":
            self.send_response(200, "OK")
            self.send_header("Content-type", "text/plain")
            self.end_headers()
            self.wfile.write(b"OK")
            return

        if self.path == "/config.js":
            self._handle_config_js()
            return

        self._apply_spa_fallback()
        super().do_GET()

    def do_HEAD(self) -> None:
        """Handles HEAD requests, routing them with SPA fallback."""
        if self.path in ("/healthz", "/config.js"):
            self.do_GET()
            return

        self._apply_spa_fallback()
        super().do_HEAD()
