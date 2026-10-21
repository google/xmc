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

"""One-shot loopback listener on 127.0.0.1 for the browser's login redirect."""

from __future__ import annotations

import hmac
import http.server
import time
import urllib.parse
from typing import Any

from xmc.auth import ACCESS_DENIED_MESSAGE, AuthError

LOOPBACK_HOST = "127.0.0.1"
_SERVER_ERR = "The XMC server failed to complete sign-in. Run `xmc login` again."
_ERROR_MESSAGES = {"access_denied": ACCESS_DENIED_MESSAGE, "server_error": _SERVER_ERR}


def parse_callback(path: str, expected_state: str) -> dict[str, str] | None:
    """Returns `{"code": ...}` or `{"error": ...}` for the genuine redirect, else None."""
    target = urllib.parse.urlsplit(path)
    query = urllib.parse.parse_qs(target.query)
    params = {name: values[0] for name, values in query.items() if len(values) == 1}
    state_ok = hmac.compare_digest(params.get("state", "").encode(), expected_state.encode())
    if target.path != "/callback" or not state_ok:
        return None
    if "error" in query:
        err = params.get("error", "")
        valid = 1 <= len(err) <= 64 and err.replace("_", "").isalnum()
        return {"error": err if valid else "unknown_error"}
    return {"code": code} if 1 <= len(code := params.get("code", "")) <= 256 else None


class _QuietHTTPServer(http.server.HTTPServer):
    def handle_error(self, *_: Any) -> None:
        """Drops per-connection errors such as a browser closing the socket early."""


class CallbackReceiver:
    """Listens on 127.0.0.1:<ephemeral port> for exactly one genuine login redirect."""

    def __init__(self, expected_state: str, success_url: str) -> None:
        """Binds the listener; the browser is sent to `success_url` once the code arrives."""
        self._result: dict[str, str] | None = None
        receiver = self

        class Handler(http.server.BaseHTTPRequestHandler):
            timeout = 2

            def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API.
                result = parse_callback(self.path, expected_state)
                self.send_response(400 if result is None else 302)
                if result is not None:
                    receiver._result = result
                    suffix = f"?{urllib.parse.urlencode(result)}" if "error" in result else ""
                    self.send_header("Location", success_url + suffix)
                self.send_header("Cache-Control", "no-store")
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
                pass

        self._server = _QuietHTTPServer((LOOPBACK_HOST, 0), Handler)

    @property
    def port(self) -> int:
        """The ephemeral port the listener is bound to."""
        return int(self._server.server_address[1])

    def wait_for_code(self, timeout: float) -> str:
        """Serves requests until the genuine redirect arrives and returns its code."""
        deadline = time.monotonic() + timeout
        while self._result is None:
            if (remaining := deadline - time.monotonic()) <= 0:
                msg = f"Timed out after {timeout:.0f}s waiting for the browser login."
                raise AuthError(f"{msg} Run `xmc login` again.")
            self._server.timeout = remaining
            self._server.handle_request()
        if "code" in self._result:
            return self._result["code"]
        error = self._result["error"]
        raise AuthError(_ERROR_MESSAGES.get(error, f"Login failed: the server reported {error!r}."))

    def close(self) -> None:
        """Releases the listening socket."""
        self._server.server_close()

    def __enter__(self) -> CallbackReceiver:
        return self

    def __exit__(self, *_: object) -> None:
        self.close()
