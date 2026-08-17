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
"""Local Python server for serving the UI, SPA routing, and mock APIs."""

import argparse
import glob
import http.server
import json
import os
import re
from typing import Any
from urllib.parse import urlparse


def get_bool_env(name: str, default: bool = False) -> bool:
    """Reads a boolean environment variable, returning default if unset."""
    val = os.environ.get(name)
    if val is None:
        return default
    return val.lower() == "true"


def find_testdata_dir() -> str | None:
    """Locates the test/dataengine/testdata/golden directory."""
    bases = [
        os.environ.get("TEST_SRCDIR", ""),
        os.environ.get("RUNFILES_DIR", ""),
        os.getcwd(),
        os.path.dirname(__file__),
    ]
    candidates: list[str] = []
    for base in bases:
        if not base:
            continue
        candidates.extend([
            os.path.join(base, "test", "dataengine", "testdata", "golden"),
            os.path.join(
                base, "_main", "test", "dataengine", "testdata", "golden"
            ),
            os.path.join(
                base,
                "xmanager_cloud",
                "test",
                "dataengine",
                "testdata",
                "golden",
            ),
            os.path.join(base, "..", "test", "dataengine", "testdata", "golden"),
            os.path.join(
                base, "..", "..", "test", "dataengine", "testdata", "golden"
            ),
            os.path.join(
                base, "..", "_main", "test", "dataengine", "testdata", "golden"
            ),
        ])
    for c in candidates:
        norm = os.path.normpath(c)
        if os.path.isdir(norm) and glob.glob(os.path.join(norm, "*.golden.json")):
            return norm
    return None


def load_golden_files() -> dict[str, Any]:
    """Loads all golden json files keyed by chart/dashboard name."""
    golden_dir = find_testdata_dir()
    if not golden_dir:
        return {}

    goldens: dict[str, Any] = {}
    for filepath in glob.glob(os.path.join(golden_dir, "*.golden.json")):
        try:
            with open(filepath, "r", encoding="utf-8") as f:
                data = json.load(f)
                if isinstance(data, dict) and "name" in data:
                    goldens[data["name"]] = data
                elif isinstance(data, list):
                    # Could be dashboards list
                    goldens[os.path.basename(filepath)] = data
        except Exception:
            continue
    return goldens


class SPAHandler(http.server.SimpleHTTPRequestHandler):
    """Custom request handler supporting SPA routing, config.js, and mock APIs."""

    goldens: dict[str, Any] = {}
    in_memory_dashboards: list[dict[str, Any]] = []

    @classmethod
    def initialize_mock_data(cls) -> None:
        """Initializes golden and in-memory mock datasets."""
        cls.goldens = load_golden_files()
        golden_dir = find_testdata_dir()
        dashboards_file = (
            os.path.join(golden_dir, "dashboards_list.golden.json")
            if golden_dir
            else ""
        )
        if os.path.exists(dashboards_file):
            try:
                with open(dashboards_file, "r", encoding="utf-8") as f:
                    cls.in_memory_dashboards = json.load(f)
            except Exception:
                cls.in_memory_dashboards = []
        else:
            cls.in_memory_dashboards = []

    def send_json(self, status_code: int, data: Any) -> None:
        """Sends a JSON response with CORS headers."""
        body = json.dumps(data).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header(
            "Access-Control-Allow-Methods",
            "GET, POST, PATCH, PUT, DELETE, OPTIONS",
        )
        self.send_header(
            "Access-Control-Allow-Headers", "Content-Type, Authorization"
        )
        self.end_headers()
        self.wfile.write(body)

    def do_OPTIONS(self) -> None:
        """Handles preflight CORS requests."""
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header(
            "Access-Control-Allow-Methods",
            "GET, POST, PATCH, PUT, DELETE, OPTIONS",
        )
        self.send_header(
            "Access-Control-Allow-Headers", "Content-Type, Authorization"
        )
        self.end_headers()

    def do_GET(self) -> None:
        """Handles GET requests for SPA routing, health checks, and mock APIs."""
        parsed_url = urlparse(self.path)
        path = parsed_url.path

        if path == "/healthz":
            self.send_response(200, "OK")
            self.send_header("Content-type", "text/plain")
            self.end_headers()
            self.wfile.write(b"OK")
            return

        if path == "/config.js":
            self._handle_config_js()
            return

        if path == "/api/user":
            self._handle_mock_user()
            return

        if path == "/v1/dashboards":
            self.send_json(200, {"dashboards": self.in_memory_dashboards})
            return

        if path.startswith("/v1/dashboards/") and ":getData" not in path:
            dashboard_id = path[len("/v1/dashboards/"):]
            for d in self.in_memory_dashboards:
                if (
                    d.get("name") == f"dashboards/{dashboard_id}"
                    or d.get("name") == dashboard_id
                ):
                    self.send_json(200, d)
                    return
            # Fallback default dashboard if not found
            if self.in_memory_dashboards:
                self.send_json(200, self.in_memory_dashboards[0])
                return
            self.send_json(404, {"error": "Dashboard not found"})
            return

        if ":getData" in path:
            self._handle_chart_data(path)
            return

        if path == "/v1/experiments":
            self._handle_experiments_list()
            return

        if path.startswith("/v1/experiments/"):
            self._handle_single_experiment(path)
            return

        if path == "/v1/clusterqueues":
            self.send_json(
                200,
                [
                    {
                        "queueName": "gpu-cluster-queue",
                        "queueCapacity": [
                            {
                                "resourceType": "nvidia.com/gpu",
                                "totalCapacity": 64,
                                "freeCapacity": 56,
                            }
                        ],
                    }
                ],
            )
            return

        translated_path = self.translate_path(self.path)
        if not os.path.exists(translated_path) or os.path.isdir(translated_path):
            serve_root = (
                self.directory
                if hasattr(self, "directory")
                else os.getcwd()
            )
            index_path = os.path.join(serve_root, "index.html")
            if os.path.exists(index_path):
                self.path = "/index.html"

        super().do_GET()

    def do_HEAD(self) -> None:
        """Handles HEAD requests using the same SPA routing fallback."""
        translated_path = self.translate_path(self.path)
        if not os.path.exists(translated_path) or os.path.isdir(translated_path):
            serve_root = (
                self.directory
                if hasattr(self, "directory")
                else os.getcwd()
            )
            index_path = os.path.join(serve_root, "index.html")
            if os.path.exists(index_path):
                self.path = "/index.html"
        super().do_HEAD()

    def do_POST(self) -> None:
        """Handles POST requests for custom methods and creating dashboards."""
        parsed_url = urlparse(self.path)
        path = parsed_url.path

        if ":getData" in path:
            self._handle_chart_data(path)
            return

        if path == "/v1/dashboards":
            try:
                length = int(self.headers.get("Content-Length", 0))
            except ValueError:
                length = 0
            body = self.rfile.read(length) if length > 0 else b"{}"
            try:
                payload = json.loads(body.decode("utf-8"))
            except Exception:
                payload = {}
            new_dash = payload.get("dashboard", payload)
            if not isinstance(new_dash, dict):
                new_dash = {}
            if "name" not in new_dash or not new_dash["name"]:
                count = len(self.in_memory_dashboards) + 1
                dash_id = payload.get("dashboardId") or f"dash_{count}"
                new_dash["name"] = f"dashboards/{dash_id}"
            self.in_memory_dashboards.append(new_dash)
            self.send_json(200, new_dash)
            return

        self.send_json(404, {"error": "Not found"})

    def _handle_config_js(self) -> None:
        """Generates dynamic runtime config.js with API url & feature flags."""
        self.send_response(200)
        self.send_header("Content-type", "application/javascript")
        self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
        self.end_headers()

        api_url = os.environ.get("XMC_API_URL", "/v1")
        refresh_interval_str = os.environ.get("XMC_REFRESH_INTERVAL_MS", "30000")
        try:
            refresh_interval = int(refresh_interval_str)
        except ValueError:
            refresh_interval = 30000

        status_history = get_bool_env("XMC_STATUS_MESSAGE_HISTORY_ENABLED", True)
        admin_panel_enabled = get_bool_env("XMC_ADMIN_PANEL_ENABLED", True)
        resource_dashboard_enabled = get_bool_env(
            "XMC_RESOURCE_DASHBOARD_ENABLED", True
        )
        performance_dashboard_enabled = get_bool_env(
            "XMC_PERFORMANCE_DASHBOARD_ENABLED", True
        )

        feature_flags = {
            "statusMessageHistoryEnabled": status_history,
            "adminPanelEnabled": admin_panel_enabled,
            "resourceDashboardEnabled": resource_dashboard_enabled,
            "performanceDashboardEnabled": performance_dashboard_enabled,
        }

        config_content = (
            f'window.XMC_API_URL="{api_url}";\n'
            f"window.XMC_REFRESH_INTERVAL_MS={refresh_interval};\n"
            f"window.XMC_FEATURE_FLAGS={json.dumps(feature_flags)};\n"
        )
        self.wfile.write(config_content.encode("utf-8"))

    def _handle_mock_user(self) -> None:
        """Serves mock authenticated user for local development."""
        user = {
            "email": "developer@google.com",
            "name": "Local Developer",
            "pictureUrl": "https://lh3.googleusercontent.com/a/default-user",
            "sessionExpiresAt": 1799999999,
            "allowedDomains": ["google.com"],
        }
        self.send_json(200, user)

    def _handle_chart_data(self, path: str) -> None:
        """Serves chart data using golden fixtures."""
        clean_path = path.split("?")[0].replace(":getData", "").lstrip("/")
        if clean_path.startswith("v1/"):
            clean_path = clean_path[3:]

        # Exact match in golden files
        if clean_path in self.goldens:
            self.send_json(200, self.goldens[clean_path])
            return

        # Match by XID or chart name
        for golden_name, golden_data in self.goldens.items():
            if golden_name.endswith(clean_path) or clean_path.endswith(
                golden_name
            ):
                self.send_json(200, golden_data)
                return

        # Fallback based on XID pattern
        match = re.search(r"experiments/(\d+)/charts/([^/]+)", clean_path)
        if match:
            xid, chart_id = match.group(1), match.group(2)
            for golden_name, golden_data in self.goldens.items():
                if (
                    f"experiments/{xid}" in golden_name
                    or chart_id in golden_name
                ):
                    self.send_json(200, golden_data)
                    return

        # Synthetic fallback
        fallback = {
            "name": clean_path,
            "plots_data": [],
        }
        self.send_json(200, fallback)

    def _handle_experiments_list(self) -> None:
        """Serves mock experiments list."""
        experiments = [
            {
                "name": "experiments/234911122",
                "xid": 234911122,
                "title": "VCC Baseline",
                "state": "COMPLETED",
            },
            {
                "name": "experiments/234911123",
                "xid": 234911123,
                "title": "VCC tuned_v1",
                "state": "RUNNING",
            },
            {
                "name": "experiments/234911124",
                "xid": 234911124,
                "title": "VCC tuned_v2",
                "state": "COMPLETED",
            },
            {
                "name": "experiments/9001",
                "xid": 9001,
                "title": "Data Engine Golden Testcases",
                "state": "COMPLETED",
            },
        ]
        self.send_json(200, {"experiments": experiments})

    def _handle_single_experiment(self, path: str) -> None:
        """Serves single experiment or work unit details."""
        clean_path = path.lstrip("/")
        if clean_path.startswith("v1/"):
            clean_path = clean_path[3:]

        exp = {
            "name": clean_path,
            "xid": 234911122,
            "title": "VCC Baseline",
            "state": "COMPLETED",
        }
        self.send_json(200, exp)


def find_dist_dir() -> str:
    """Finds the Angular build distribution directory."""
    candidates = [
        os.path.join(os.path.dirname(__file__), "dist", "browser"),
        os.path.join(os.path.dirname(__file__), "dist"),
        os.path.join(os.getcwd(), "cmd", "ui", "dist", "browser"),
        os.path.join(os.getcwd(), "dist", "browser"),
        os.path.join(os.getcwd(), "dist"),
        os.getcwd(),
    ]
    for c in candidates:
        norm = os.path.normpath(c)
        if os.path.exists(os.path.join(norm, "index.html")):
            return norm
    return os.path.normpath(candidates[0])


def main() -> None:
    """Main entrypoint running the local UI server."""
    parser = argparse.ArgumentParser(
        description="XMC UI and Mock API Server"
    )
    parser.add_argument(
        "--port",
        type=int,
        default=8080,
        help="Port to listen on (default: 8080)",
    )
    parser.add_argument(
        "--dir",
        type=str,
        default="",
        help="Directory to serve static files from",
    )
    args = parser.parse_args()

    serve_dir = args.dir or find_dist_dir()
    SPAHandler.initialize_mock_data()

    def handler_factory(*h_args: Any, **h_kwargs: Any) -> SPAHandler:
        return SPAHandler(*h_args, directory=serve_dir, **h_kwargs)

    server_address = ("0.0.0.0", args.port)
    httpd = http.server.ThreadingHTTPServer(server_address, handler_factory)

    print(f"Starting XMC Local UI Server on http://localhost:{args.port}...")
    print(f"Serving static assets from: {serve_dir}")
    print(
        f"Mock testdata loaded: {len(SPAHandler.goldens)} golden files, "
        f"{len(SPAHandler.in_memory_dashboards)} dashboards"
    )
    print("Press Ctrl+C to stop.")

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down server.")
        httpd.server_close()


if __name__ == "__main__":
    main()
