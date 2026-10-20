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

"""One-shot loopback listener that receives the browser's login redirect.

It binds 127.0.0.1 on an ephemeral port and accepts only `GET /callback` with a
constant-time `state` match. Anything else gets a 400 and is ignored, so a
forged request cannot cancel a login in progress.
"""

from __future__ import annotations

import hmac
import http.server
import time
import urllib.parse
from types import TracebackType
from typing import Any

from xmc.auth.errors import ACCESS_DENIED_MESSAGE, AuthError

LOOPBACK_HOST = "127.0.0.1"
_CONNECTION_TIMEOUT_SECONDS = 2
_ERROR_MESSAGES = {
    "access_denied": ACCESS_DENIED_MESSAGE,
    "server_error": "The XMC server failed to complete sign-in. Run `xmc login` again.",
}


def parse_callback(path: str, expected_state: str) -> dict[str, str] | None:
    """Returns `{"code": ...}` or `{"error": ...}` for the genuine redirect, else None.

    Any `error` (RFC 6749 section 4.1.2.1) with a matching state is terminal;
    values outside a safe charset are reported as `unknown_error`.

    Args:
        path: Raw request target, e.g. `/callback?code=...&state=...`.
        expected_state: The `state` generated for this login attempt.
    """
    target = urllib.parse.urlsplit(path)
    query = urllib.parse.parse_qs(target.query)
    params = {name: values[0] for name, values in query.items() if len(values) == 1}
    if target.path != "/callback":
        return None
    if not hmac.compare_digest(params.get("state", "").encode(), expected_state.encode()):
        return None
    if "error" in query:
        error = params.get("error", "")
        valid_error = 1 <= len(error) <= 64 and error.replace("_", "").isalnum()
        return {"error": error if valid_error else "unknown_error"}
    code = params.get("code", "")
    return {"code": code} if 1 <= len(code) <= 256 else None


class _QuietHTTPServer(http.server.HTTPServer):
    def handle_error(self, request: Any, client_address: Any) -> None:
        """Drops per-connection errors such as a browser closing the socket early."""


class CallbackReceiver:
    """Listens on 127.0.0.1:<ephemeral port> for exactly one genuine login redirect."""

    def __init__(self, expected_state: str, success_url: str) -> None:
        """Binds the listener; the browser is sent to `success_url` once the code arrives."""
        self._expected_state = expected_state
        self._success_url = success_url
        self._result: dict[str, str] | None = None
        receiver = self

        class Handler(http.server.BaseHTTPRequestHandler):
            # A stalled connection can delay the deadline by at most this long.
            timeout = _CONNECTION_TIMEOUT_SECONDS

            def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API.
                receiver._on_request(self)

            def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
                pass  # The default logs the request line, which carries the code.

        self._server = _QuietHTTPServer((LOOPBACK_HOST, 0), Handler)

    @property
    def port(self) -> int:
        """The ephemeral port the listener is bound to."""
        return int(self._server.server_address[1])

    def _on_request(self, handler: http.server.BaseHTTPRequestHandler) -> None:
        """Replies 302 to the genuine redirect (recording it) and 400 to anything else."""
        result = parse_callback(handler.path, self._expected_state)
        handler.send_response(400 if result is None else 302)
        if result is not None:
            self._result = result
            query = f"?{urllib.parse.urlencode(result)}" if "error" in result else ""
            handler.send_header("Location", self._success_url + query)
        handler.send_header("Cache-Control", "no-store")
        handler.send_header("Content-Length", "0")
        handler.end_headers()

    def wait_for_code(self, timeout: float) -> str:
        """Serves requests until the genuine redirect arrives and returns its code.

        Raises:
            AuthError: The deployment denied or failed the login, or time ran out.
        """
        deadline = time.monotonic() + timeout
        while self._result is None:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AuthError(
                    f"Timed out after {timeout:.0f}s waiting for the browser login. "
                    "Run `xmc login` again."
                )
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

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.close()
