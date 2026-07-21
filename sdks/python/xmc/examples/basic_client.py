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

"""Example demonstrating usage of the XMC Python SDK HTTP Client."""

import http.server
import json
import socketserver
import threading
import urllib.parse
from typing import Any

import xmc


class MockXMCServerHandler(http.server.BaseHTTPRequestHandler):
    """Simple HTTP request handler mocking XMC Metrics API responses."""

    def do_GET(self) -> None:
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/v1/metrics":
            query = urllib.parse.parse_qs(parsed.query)
            payload = {
                "status": "success",
                "query_params_received": query,
                "metrics": [
                    {"metric_name": "train/loss", "metric_value": 0.042, "step": 100},
                    {"metric_name": "train/accuracy", "metric_value": 0.985, "step": 100},
                ],
            }
            body = json.dumps(payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_error(404, "Endpoint Not Found")

    def do_POST(self) -> None:
        content_length = int(self.headers.get("Content-Length", 0))
        raw_body = self.rfile.read(content_length).decode("utf-8")
        data = json.loads(raw_body) if raw_body else {}

        if self.path == "/v1/metrics":
            response_payload = {
                "status": "created",
                "metric_id": "m-12345",
                "received_data": data,
            }
            body = json.dumps(response_payload).encode("utf-8")
            self.send_response(201)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_error(400, "Invalid Path")

    def log_message(self, format_str: str, *args: Any) -> None:
        # Silence default server logging for clean output
        pass


def run_mock_server() -> socketserver.TCPServer:
    server = socketserver.TCPServer(("127.0.0.1", 0), MockXMCServerHandler)
    server_thread = threading.Thread(target=server.serve_forever, daemon=True)
    server_thread.start()
    return server


def main() -> None:
    print("=== XMC Python SDK Client Example ===")

    # 1. Start local mock HTTP server
    server = run_mock_server()
    host, port = server.server_address
    base_url = f"http://{host}:{port}"
    print(f"[1] Mock XMC Metrics server started at {base_url}")

    # 2. Instantiate XMC Client
    print("\n[2] Instantiating XMC Client...")
    with xmc.Client(base_url=base_url, api_key="sample-api-token") as client:
        # 3. Perform GET request with multi-value query parameters
        print("\n[3] Sending GET request (fetching metrics with sequence params)...")
        get_response = client.get("v1/metrics", params={"step": "100", "tag": ["loss", "accuracy"]})
        print("    GET Response:")
        print(f"    {json.dumps(get_response, indent=2)}")

        # 4. Perform POST request with metric payload
        print("\n[4] Sending POST request (logging new metric)...")
        post_payload = {
            "metric_name": "train/loss",
            "metric_value": 0.042,
            "step": 100,
        }
        post_response = client.post("v1/metrics", data=post_payload)
        print("    POST Response:")
        print(f"    {json.dumps(post_response, indent=2)}")

        # 5. Demonstrate API error handling (404 Not Found)
        print("\n[5] Demonstrating API Error handling (requesting invalid endpoint)...")
        try:
            client.get("v1/invalid_endpoint")
        except xmc.XMCAPIError as err:
            print(f"    Caught expected XMCAPIError: {err}")
            print(f"    Status Code: {err.status_code}")

    # Shutdown mock server
    server.shutdown()
    server.server_close()
    print("\n=== Example Completed Successfully ===")


if __name__ == "__main__":
    main()
