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

"""Unit tests for xmc.auth.token_client."""

from __future__ import annotations

import email.message
import http.server
import io
import json
import os
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from typing import Any
from unittest import mock

from xmc.auth import token_client
from xmc.auth.errors import AuthError
from xmc.auth.token_client import TokenGrant

_SERVER = "https://xmc.example.com"
_GRANT_JSON = {
    "access_token": "tok-123",
    "token_type": "Bearer",
    "expires_in": 43200,
    "email": "user@example.com",
}


def _ok(payload: Any) -> mock.MagicMock:
    """Builds an opener whose response body is `payload` serialized as JSON."""
    opener = mock.MagicMock()
    opener.open.return_value.__enter__.return_value.read.return_value = json.dumps(payload).encode()
    return opener


def _failing(error: Exception) -> mock.MagicMock:
    """Builds an opener whose open() raises `error`."""
    opener = mock.MagicMock()
    opener.open.side_effect = error
    return opener


def _http_error(status: int, body: bytes = b"") -> mock.MagicMock:
    """Builds an opener that raises HTTPError with the given status and body."""
    message = email.message.Message()
    return _failing(urllib.error.HTTPError(_SERVER, status, "error", message, io.BytesIO(body)))


class _RedirectingServer(http.server.HTTPServer):
    """A loopback server that answers every POST with a 302 to another origin."""

    def __init__(self) -> None:
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API.
                self.send_response(302)
                self.send_header("Location", "https://attacker.example/steal")
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
                pass

        super().__init__(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.serve_forever, daemon=True).start()

    @property
    def origin(self) -> str:
        return f"http://127.0.0.1:{self.server_address[1]}"


class BuildOpenerTest(unittest.TestCase):
    """Tests opener hardening: redirects and proxies."""

    def test_loopback_bypasses_proxies(self) -> None:
        """Loopback origins never go through an environment-configured proxy."""
        env = {
            "http_proxy": "http://proxy.example:3128",
            "https_proxy": "http://proxy.example:3128",
        }
        with mock.patch.dict(os.environ, env):
            for origin, want_proxies in (("http://127.0.0.1:8080", False), (_SERVER, True)):
                opener = token_client.build_opener(origin)
                proxies = [
                    getattr(h, "proxies", None)
                    for h in opener.handlers
                    if isinstance(h, urllib.request.ProxyHandler)
                ]
                with self.subTest(origin=origin):
                    self.assertEqual(any(proxies), want_proxies)

    def test_refuses_redirects(self) -> None:
        """Token exchange fails on a 302 instead of following it."""
        server = _RedirectingServer()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        with self.assertRaisesRegex(AuthError, r"redirect \(HTTP 302\)"):
            token_client.exchange_code(server.origin, "c", "v")


class ExchangeCodeTest(unittest.TestCase):
    """Tests the token exchange request and its error mapping."""

    def test_success_request_shape(self) -> None:
        """The exchange POSTs the form contract to the token endpoint."""
        opener = _ok(_GRANT_JSON)
        grant = token_client.exchange_code(_SERVER, "the-code", "the-verifier", opener=opener)
        self.assertEqual(grant, TokenGrant("tok-123", "user@example.com", 43200))
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, "https://xmc.example.com/api/cli/token")
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(request.get_header("Content-type"), "application/x-www-form-urlencoded")
        self.assertEqual(
            urllib.parse.parse_qs(request.data.decode()),
            {
                "grant_type": ["authorization_code"],
                "code": ["the-code"],
                "code_verifier": ["the-verifier"],
            },
        )

    def test_error_mapping(self) -> None:
        """HTTP and network failures map to actionable messages without chaining."""
        cases = [
            (_http_error(400, b'{"error":"invalid_grant"}'), "invalid, expired"),
            (_http_error(403, b'{"error":"access_denied"}'), "not authorized"),
            (_http_error(403, b"not json"), "not authorized"),
            (_http_error(429, b'{"error":"too_many_requests"}'), "Too many"),
            (_http_error(400, b'{"error":"too_many_requests"}'), "Too many"),
            (_http_error(500, b"[]"), "HTTP 500"),
            (_failing(urllib.error.URLError("refused")), "Could not reach.*refused"),
            (_failing(TimeoutError("slow")), "Could not reach"),
        ]
        for opener, pattern in cases:
            with self.subTest(pattern=pattern), self.assertRaisesRegex(AuthError, pattern) as ctx:
                token_client.exchange_code(_SERVER, "c", "v", opener=opener)
            self.assertTrue(ctx.exception.__suppress_context__)

    def test_errors_never_echo_secrets(self) -> None:
        """Error messages never include the code or verifier."""
        opener = _http_error(400, b'{"error":"invalid_grant"}')
        with self.assertRaises(AuthError) as ctx:
            token_client.exchange_code(_SERVER, "secret-code", "secret-verifier", opener=opener)
        self.assertNotIn("secret", str(ctx.exception))


class ParseTokenResponseTest(unittest.TestCase):
    """Tests token response validation."""

    def test_rejects_malformed(self) -> None:
        """Responses missing fields or with wrong types are rejected."""
        cases = {
            "not json": b"{",
            "list": b"[]",
            "no token": {**_GRANT_JSON, "access_token": ""},
            "wrong type": {**_GRANT_JSON, "token_type": "MAC"},
            "no email": {k: v for k, v in _GRANT_JSON.items() if k != "email"},
            "zero ttl": {**_GRANT_JSON, "expires_in": 0},
            "bool ttl": {**_GRANT_JSON, "expires_in": True},
            "str ttl": {**_GRANT_JSON, "expires_in": "3600"},
        }
        for name, payload in cases.items():
            raw = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
            with self.subTest(name), self.assertRaisesRegex(AuthError, "malformed"):
                token_client.parse_token_response(raw)

    def test_token_type_is_case_insensitive(self) -> None:
        """`bearer` is accepted regardless of case."""
        raw = json.dumps({**_GRANT_JSON, "token_type": "bearer"}).encode()
        self.assertEqual(token_client.parse_token_response(raw).access_token, "tok-123")


if __name__ == "__main__":
    unittest.main()
