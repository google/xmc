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
"""
A standalone Python server for local UI development with mock APIs and SPA fallback.
This is intentionally scoped as a minimal mock setup for the performance dashboards
and metrics workflow for now rather than an exhaustive mock of all XMC endpoints
(such as /v1/clusterqueues).
There is parallel ongoing work around running the UI locally,
so this implementation serves as the starting point to establish
and consolidate those local development workflows.
Additional endpoints can be incrementally added
and unified as those parallel efforts converge.
"""

import argparse
import contextlib
import glob
import http.server
import json
import logging
import os
import re
import sys
from typing import TYPE_CHECKING, Any, ClassVar

logger = logging.getLogger(__name__)

# Ensure cmd/ui and repo root are in sys.path for direct python execution
_repo_root = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
_cmd_ui_dir = os.path.normpath(os.path.join(_repo_root, "cmd", "ui"))
if _cmd_ui_dir not in sys.path:
    sys.path.insert(0, _cmd_ui_dir)
if _repo_root not in sys.path:
    sys.path.insert(0, _repo_root)

if TYPE_CHECKING:
    import server
else:
    try:
        import server
    except ImportError:
        try:
            from cmd.ui import server
        except ImportError:
            from xmanager_cloud.ui import server

get_bool_env = server.get_bool_env


def find_testdata_dirs() -> list[str]:
    """Locates all test fixtures and golden directories."""
    bases = [
        os.environ.get("TEST_SRCDIR", ""),
        os.environ.get("RUNFILES_DIR", ""),
        os.getcwd(),
        os.path.dirname(__file__),
    ]
    subpaths = [
        os.path.join("test", "fixtures", "dashboards"),
        os.path.join("test", "dataengine", "testdata", "golden"),
        os.path.join("cmd", "ui", "testdata"),
    ]
    found: list[str] = []
    seen: set[str] = set()
    for base in bases:
        if not base:
            continue
        for sub in subpaths:
            candidates = [
                os.path.join(base, sub),
                os.path.join(base, "_main", sub),
                os.path.join(base, "xmanager_cloud", sub),
                os.path.join(base, "..", sub),
                os.path.join(base, "..", "..", sub),
                os.path.join(base, "..", "..", "..", sub),
                os.path.join(base, "..", "_main", sub),
                os.path.join(base, "..", "..", "_main", sub),
                os.path.join(base, "..", "..", "..", "_main", sub),
                os.path.join(base, "..", "xmanager_cloud", sub),
                os.path.join(base, "..", "..", "xmanager_cloud", sub),
                os.path.join(base, "..", "..", "..", "xmanager_cloud", sub),
            ]
            for c in candidates:
                norm = os.path.normpath(c)
                if (
                    norm not in seen
                    and os.path.isdir(norm)
                    and (
                        glob.glob(os.path.join(norm, "*.json"))
                        or glob.glob(os.path.join(norm, "*.golden.json"))
                    )
                ):
                    seen.add(norm)
                    found.append(norm)
    return found


def find_testdata_dir() -> str | None:
    """Locates the primary test fixtures or golden directory."""
    dirs = find_testdata_dirs()
    return dirs[0] if dirs else None


def load_golden_files() -> dict[str, Any]:
    """Loads all mock/golden json files keyed by chart/dashboard name."""
    goldens: dict[str, Any] = {}
    for golden_dir in find_testdata_dirs():
        patterns = [
            os.path.join(golden_dir, "*.golden.json"),
            os.path.join(golden_dir, "*.json"),
        ]
        for pattern in patterns:
            for filepath in glob.glob(pattern):
                try:
                    with open(filepath, encoding="utf-8") as f:
                        data = json.load(f)
                        if isinstance(data, dict) and "name" in data:
                            goldens[data["name"]] = data
                        elif isinstance(data, list):
                            goldens[os.path.basename(filepath)] = data
                        else:
                            logger.warning(
                                "Skipping '%s': expected a dict with a 'name' key or a list.",
                                filepath,
                            )
                except (json.JSONDecodeError, OSError, UnicodeDecodeError) as e:
                    logger.warning("Failed to load golden file '%s': %s", filepath, e)
                    continue
    return goldens


# server.SPAHandler is Any because server is loaded through sys.path; unused-ignore keeps
# environments where it resolves passing.
class SPAHandler(server.SPAHandler):  # type: ignore[misc, unused-ignore]
    """Custom request handler supporting SPA routing, config.js, and mock APIs."""

    goldens: ClassVar[dict[str, Any]] = {}
    in_memory_dashboards: ClassVar[list[dict[str, Any]]] = []

    @classmethod
    def initialize_mock_data(cls) -> None:
        """Initializes golden and in-memory mock datasets."""
        cls.goldens = load_golden_files()
        cls.in_memory_dashboards = []
        for golden_dir in find_testdata_dirs():
            for fname in ["dashboards_list.json", "dashboards_list.golden.json"]:
                dashboards_file = os.path.join(golden_dir, fname)
                if os.path.exists(dashboards_file):
                    try:
                        with open(dashboards_file, encoding="utf-8") as f:
                            data = json.load(f)
                            if isinstance(data, list) and data:
                                cls.in_memory_dashboards = data
                                return
                    except (json.JSONDecodeError, OSError, UnicodeDecodeError):
                        pass

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
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")
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
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")
        self.end_headers()

    def do_GET(self) -> None:
        """Handles GET requests for SPA routing, static files, and mock APIs."""
        if self.path == "/healthz":
            self.send_response(200, "OK")
            self.send_header("Content-Type", "text/plain")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(b"OK")
            return

        if self.path == "/config.js":
            self._handle_config_js()
            return

        if self.path.startswith("/v1/auth/user") or self.path.startswith("/auth/user"):
            self._handle_auth_user()
            return

        if ":getData" in self.path:
            self._handle_chart_data(self.path)
            return

        if self.path.startswith("/v1/dashboards") or self.path.startswith("/dashboards"):
            clean_path = self.path.split("?")[0].rstrip("/")
            if clean_path in ("/v1/dashboards", "/dashboards"):
                self.send_json(200, {"dashboards": SPAHandler.in_memory_dashboards})
                return
            dashboard_name = clean_path.lstrip("/").removeprefix("v1/")
            for dash in SPAHandler.in_memory_dashboards:
                if dash.get("name") == dashboard_name:
                    self.send_json(200, dash)
                    return
            self.send_json(404, {"error": f"Dashboard {dashboard_name} not found"})
            return

        if self.path.startswith("/v1/experiments") or self.path.startswith("/experiments"):
            clean_path = self.path.split("?")[0].rstrip("/")
            if clean_path in ("/v1/experiments", "/experiments"):
                self._handle_experiments_list()
                return
            self._handle_single_experiment(clean_path)
            return

        super().do_GET()

    def do_HEAD(self) -> None:
        """Handles HEAD requests, routing them with SPA fallback."""
        if self.path in ("/healthz", "/config.js") or self.path.startswith(("/v1/", "/auth/")):
            self.do_GET()
            return

        super().do_HEAD()

    def do_POST(self) -> None:
        """Handles POST requests for creating resources."""
        if self.path.startswith("/v1/dashboards") or self.path.startswith("/dashboards"):
            self._handle_dashboards_post()
            return
        self.send_json(404, {"error": "Not Found"})

    def do_DELETE(self) -> None:
        """Handles DELETE requests for deleting resources."""
        if self.path.startswith("/v1/dashboards") or self.path.startswith("/dashboards"):
            self._handle_dashboards_delete()
            return
        self.send_json(404, {"error": "Not Found"})

    def _handle_dashboards_post(self) -> None:
        """Creates or updates a dashboard in-memory."""
        content_length = int(self.headers.get("Content-Length", 0))
        post_data = self.rfile.read(content_length)
        try:
            payload = json.loads(post_data.decode("utf-8")) if post_data else {}
        except json.JSONDecodeError:
            self.send_json(400, {"error": "Invalid JSON payload"})
            return

        if not isinstance(payload, dict):
            self.send_json(400, {"error": "Invalid JSON payload: expected a JSON object"})
            return

        dashboard_id = (
            payload.get("dashboardId") or f"dashboard-{len(SPAHandler.in_memory_dashboards) + 1}"
        )
        new_dashboard = {
            "name": f"dashboards/{dashboard_id}",
            "title": payload.get("title", "New Dashboard"),
            "description": payload.get("description", ""),
            "charts": payload.get("charts", []),
            "owner": payload.get("owner", "local-dev@google.com"),
            "create_time": "2026-08-18T00:00:00Z",
            "update_time": "2026-08-18T00:00:00Z",
        }
        SPAHandler.in_memory_dashboards.append(new_dashboard)
        self.send_json(200, new_dashboard)

    def _handle_dashboards_delete(self) -> None:
        """Deletes a dashboard from in-memory store."""
        clean_path = self.path.split("?")[0].lstrip("/").removeprefix("v1/")
        initial_len = len(SPAHandler.in_memory_dashboards)
        SPAHandler.in_memory_dashboards = [
            d for d in SPAHandler.in_memory_dashboards if d.get("name") != clean_path
        ]
        if len(SPAHandler.in_memory_dashboards) < initial_len:
            self.send_json(200, {"message": f"Dashboard {clean_path} deleted successfully"})
        else:
            self.send_json(404, {"error": f"Dashboard {clean_path} not found"})

    def _handle_config_js(self) -> None:
        """Generates dynamic runtime config.js matching deployment settings."""
        self.send_response(200)
        self.send_header("Content-Type", "application/javascript")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        config_content = server.generate_runtime_config(default_api_url="/v1")
        self.wfile.write(config_content.encode("utf-8"))

    def _handle_auth_user(self) -> None:
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
        clean_path = path.split("?")[0].replace(":getData", "").lstrip("/").removeprefix("v1/")

        # Exact match in golden files
        if clean_path in SPAHandler.goldens:
            self.send_json(200, SPAHandler.goldens[clean_path])
            return

        # Match by XID or chart name
        for golden_name, golden_data in SPAHandler.goldens.items():
            if golden_name.endswith(clean_path) or clean_path.endswith(golden_name):
                self.send_json(200, golden_data)
                return

        # Fallback based on XID pattern
        match = re.search(r"experiments/(\d+)/charts/([^/]+)", clean_path)
        if match:
            xid, chart_id = match.group(1), match.group(2)
            for golden_name, golden_data in SPAHandler.goldens.items():
                if f"experiments/{xid}" in golden_name and chart_id in golden_name:
                    self.send_json(200, golden_data)
                    return

        # Synthetic fallback
        fallback = {
            "name": clean_path,
            "plots_data": [],
        }
        self.send_json(200, fallback)

    @classmethod
    def get_experiments(cls) -> list[dict[str, Any]]:
        """Dynamically extracts distinct experiments from loaded test datasets."""
        experiments: dict[int, dict[str, Any]] = {}

        # 1. Extract from loaded dashboards
        for dash in cls.in_memory_dashboards:
            for chart in dash.get("charts", []):
                xid = chart.get("xid")
                if xid is None:
                    continue
                try:
                    xid_int = int(xid)
                except (ValueError, TypeError):
                    continue
                if xid_int not in experiments:
                    title = chart.get("title") or f"Experiment {xid_int}"
                    state = "RUNNING" if "tuned_v1" in str(chart.get("name", "")) else "COMPLETED"
                    experiments[xid_int] = {
                        "name": f"experiments/{xid_int}",
                        "xid": xid_int,
                        "title": title,
                        "state": state,
                    }

        # 2. Extract from loaded golden chart files
        for golden_name, golden_data in cls.goldens.items():
            if not isinstance(golden_data, dict):
                continue
            xid = golden_data.get("xid")
            golden_xid: int | None = None
            if xid is not None:
                with contextlib.suppress(ValueError, TypeError):
                    golden_xid = int(xid)
            if golden_xid is None:
                match = re.search(r"experiments/(\d+)", golden_name)
                if match:
                    golden_xid = int(match.group(1))

            if golden_xid is not None and golden_xid not in experiments:
                experiments[golden_xid] = {
                    "name": f"experiments/{golden_xid}",
                    "xid": golden_xid,
                    "title": f"Experiment {golden_xid}",
                    "state": "COMPLETED",
                }

        # 3. Fallback baseline if no test datasets are found
        if not experiments:
            return [
                {
                    "name": "experiments/234911122",
                    "xid": 234911122,
                    "title": "VCC Baseline",
                    "state": "COMPLETED",
                },
                {
                    "name": "experiments/234911123",
                    "xid": 234911123,
                    "title": "VCC Tuned LR",
                    "state": "RUNNING",
                },
                {
                    "name": "experiments/234911124",
                    "xid": 234911124,
                    "title": "VCC Tuned Batch Size",
                    "state": "COMPLETED",
                },
                {
                    "name": "experiments/9001",
                    "xid": 9001,
                    "title": "Data Engine Golden Testcases",
                    "state": "COMPLETED",
                },
            ]

        return sorted(experiments.values(), key=lambda e: e["xid"])

    def _handle_experiments_list(self) -> None:
        """Serves mock experiments list dynamically extracted from test data."""
        self.send_json(200, {"experiments": self.get_experiments()})

    def _handle_single_experiment(self, path: str) -> None:
        """Serves single experiment or work unit details."""
        clean_path = path.lstrip("/")
        if clean_path.startswith("v1/"):
            clean_path = clean_path[3:]

        match = re.search(r"experiments/(\d+)", clean_path)
        if match:
            xid_int = int(match.group(1))
            for exp in self.get_experiments():
                if exp.get("xid") == xid_int:
                    self.send_json(200, exp)
                    return
            exp = {
                "name": f"experiments/{xid_int}",
                "xid": xid_int,
                "title": f"Experiment {xid_int}",
                "state": "COMPLETED",
            }
            self.send_json(200, exp)
            return

        self.send_json(404, {"error": "Experiment not found"})


def find_dist_dir() -> str:
    """Finds the Angular build distribution directory."""
    this_dir = os.path.dirname(__file__)
    candidates = [
        os.path.join(this_dir, "..", "..", "..", "cmd", "ui", "dist", "browser"),
        os.path.join(this_dir, "..", "..", "..", "cmd", "ui", "dist"),
        os.path.join(os.getcwd(), "cmd", "ui", "dist", "browser"),
        os.path.join(os.getcwd(), "cmd", "ui", "dist"),
        os.path.join(os.getcwd(), "dist", "browser"),
        os.path.join(os.getcwd(), "dist"),
        os.path.join(this_dir, "dist", "browser"),
        os.path.join(this_dir, "dist"),
        os.getcwd(),
    ]
    for c in candidates:
        norm = os.path.normpath(c)
        if os.path.exists(os.path.join(norm, "index.html")):
            return norm
    return os.path.normpath(candidates[0])


def main() -> None:
    """Main entrypoint running the local UI server."""
    parser = argparse.ArgumentParser(description="XMC UI and Mock API Server")
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
