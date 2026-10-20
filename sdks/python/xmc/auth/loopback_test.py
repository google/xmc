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

"""Unit tests for xmc.auth.loopback."""

from __future__ import annotations

import http.client
import socket
import threading
import time
import unittest
from unittest import mock

from xmc.auth import loopback
from xmc.auth.errors import ACCESS_DENIED_MESSAGE, AuthError

_STATE = "s" * 43
_CODE = "c" * 43
_PORT = 5000
_HOST = f"127.0.0.1:{_PORT}"
_SUCCESS_URL = "https://xmc.example.com/api/cli/success"


class ParseCallbackTest(unittest.TestCase):
    """Tests request classification in isolation."""

    def _parse(self, path: str, host: str | None = _HOST) -> dict[str, str] | None:
        return loopback.parse_callback(path, host, _PORT, _STATE)

    def test_genuine_redirects(self) -> None:
        """Matching redirects yield the code or the server's error."""
        cases = {
            f"/callback?code={_CODE}&state={_STATE}": {"code": _CODE},
            f"/callback?error=access_denied&state={_STATE}": {"error": "access_denied"},
            f"/callback?error=server_error&code={_CODE}&state={_STATE}": {"error": "server_error"},
            f"/callback?error=%3Cscript%3E&state={_STATE}": {"error": "unknown_error"},
            f"/callback?error=a&error=b&state={_STATE}": {"error": "unknown_error"},
        }
        for path, want in cases.items():
            with self.subTest(path=path):
                self.assertEqual(self._parse(path), want)

    def test_ignored(self) -> None:
        """Forged, malformed, or misdirected requests are ignored."""
        good = f"code={_CODE}&state={_STATE}"
        cases = [
            (f"/other?{good}", _HOST),
            ("/favicon.ico", _HOST),
            (f"/callback?{good}", "localhost:5000"),
            (f"/callback?{good}", "attacker.example:5000"),
            (f"/callback?{good}", None),
            (f"/callback?code={_CODE}&state=wrong", _HOST),
            (f"/callback?code={_CODE}", _HOST),
            (f"/callback?{good}&state={_STATE}", _HOST),
            (f"/callback?code=bad%20code&state={_STATE}", _HOST),
            (f"/callback?state={_STATE}", _HOST),
            ("/callback?error=access_denied&state=wrong", _HOST),
            ("/callback?code=x&state=%C3%A9", _HOST),
        ]
        for path, host in cases:
            with self.subTest(path=path, host=host):
                self.assertIsNone(self._parse(path, host))


class CallbackReceiverTest(unittest.TestCase):
    """Exercises the listener over real loopback sockets."""

    def setUp(self) -> None:
        self.receiver = loopback.CallbackReceiver(_STATE, _SUCCESS_URL)
        self.addCleanup(self.receiver.close)
        self.result: str | None = None
        self.error: BaseException | None = None
        self.thread = threading.Thread(target=self._wait, daemon=True)
        self.thread.start()

    def _wait(self) -> None:
        try:
            self.result = self.receiver.wait_for_code(timeout=10)
        except BaseException as e:  # Surfaced to the test thread.
            self.error = e

    def _get(self, path: str, host: str | None = None) -> http.client.HTTPResponse:
        conn = http.client.HTTPConnection(loopback.LOOPBACK_HOST, self.receiver.port, timeout=5)
        self.addCleanup(conn.close)
        conn.request("GET", path, headers={"Host": host or f"127.0.0.1:{self.receiver.port}"})
        response = conn.getresponse()
        response.read()
        return response

    def _join(self) -> None:
        self.thread.join(timeout=10)
        self.assertFalse(self.thread.is_alive())

    def test_success_redirects_to_success_page(self) -> None:
        """The genuine redirect returns the code and 302s to the success page."""
        self.assertGreater(self.receiver.port, 0)
        response = self._get(f"/callback?code={_CODE}&state={_STATE}")
        self._join()
        self.assertEqual(response.status, 302)
        self.assertEqual(response.getheader("Location"), _SUCCESS_URL)
        self.assertEqual(response.getheader("Cache-Control"), "no-store")
        self.assertEqual(self.result, _CODE)

    def test_forgeries_do_not_abort_login(self) -> None:
        """State mismatches, wrong Host headers, and wrong paths get 400; the wait continues."""
        self.assertEqual(self._get("/callback?code=evil&state=wrong").status, 400)
        self.assertEqual(
            self._get(f"/callback?code=evil&state={_STATE}", host="evil.example").status, 400
        )
        self.assertEqual(self._get("/").status, 400)
        self.assertTrue(self.thread.is_alive())
        self._get(f"/callback?code={_CODE}&state={_STATE}")
        self._join()
        self.assertEqual(self.result, _CODE)

    def test_access_denied_fails_fast(self) -> None:
        """An access_denied redirect fails the login and forwards the error."""
        response = self._get(f"/callback?error=access_denied&state={_STATE}")
        self._join()
        self.assertEqual(response.getheader("Location"), f"{_SUCCESS_URL}?error=access_denied")
        self.assertIsInstance(self.error, AuthError)
        self.assertEqual(str(self.error), ACCESS_DENIED_MESSAGE)

    def _assert_error_fails_fast(self, error: str, message: str) -> None:
        response = self._get(f"/callback?error={error}&state={_STATE}")
        self._join()
        self.assertEqual(response.getheader("Location"), f"{_SUCCESS_URL}?error={error}")
        self.assertIsInstance(self.error, AuthError)
        self.assertIn(message, str(self.error))

    def test_server_error_fails_fast(self) -> None:
        """server_error fails the login with a retry hint."""
        self._assert_error_fails_fast("server_error", "failed to complete sign-in")

    def test_unknown_error_fails_fast(self) -> None:
        """Other error codes fail the login, naming the code."""
        self._assert_error_fails_fast("temporarily_unavailable", "'temporarily_unavailable'")


class CallbackReceiverTimeoutTest(unittest.TestCase):
    """Tests the login deadline."""

    def test_times_out(self) -> None:
        """No redirect before the deadline raises a timeout error."""
        with loopback.CallbackReceiver(_STATE, _SUCCESS_URL) as receiver:
            with self.assertRaisesRegex(AuthError, "Timed out"):
                receiver.wait_for_code(timeout=0.05)

    def test_deadline_uses_injected_clock(self) -> None:
        """An already-expired deadline raises without serving any request."""
        clock = mock.Mock(side_effect=[100.0, 400.0])
        with loopback.CallbackReceiver(_STATE, _SUCCESS_URL) as receiver:
            with self.assertRaisesRegex(AuthError, "Timed out"):
                receiver.wait_for_code(timeout=300, clock=clock)

    def test_stalled_connection_cannot_extend_deadline(self) -> None:
        """A client that connects but never sends delays the deadline by at most ~2s."""
        with loopback.CallbackReceiver(_STATE, _SUCCESS_URL) as receiver:
            stalled = socket.create_connection((loopback.LOOPBACK_HOST, receiver.port))
            self.addCleanup(stalled.close)
            start = time.monotonic()
            with self.assertRaisesRegex(AuthError, "Timed out"):
                receiver.wait_for_code(timeout=0.2)
            self.assertLess(time.monotonic() - start, 0.2 + 2 + 1)


if __name__ == "__main__":
    unittest.main()
