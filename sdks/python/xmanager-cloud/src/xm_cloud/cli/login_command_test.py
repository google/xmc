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
"""Unit tests for `xm_cloud.cli.login_command`."""

from __future__ import annotations

import base64
import contextlib
import datetime
import email.message
import hashlib
import http.client
import io
import json
import os
import socket
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
import urllib.response
from collections.abc import Callable, Iterator, Mapping
from unittest import mock

from xm_cloud.cli import credentials, login_command, logout_command

_SERVER = "https://xmc.example.com"
_GOOD_GRANT = {
    "access_token": "jwt-token",
    "token_type": "Bearer",
    "expires_in": 3600,
    "email": "researcher@example.com",
}


class _FailingBody(io.BytesIO):
    """Response body whose reads fail, e.g. when the connection drops mid-body."""

    def __init__(self, exc: BaseException) -> None:
        super().__init__()
        self._exc = exc

    def read(self, size: int | None = -1) -> bytes:
        del size
        raise self._exc


class _FakeResponse(urllib.response.addinfourl):
    """Canned HTTPS response that flows through the real urllib handler chain."""

    def __init__(self, url: str, code: int, body: io.BytesIO, headers: Mapping[str, str]) -> None:
        message = email.message.Message()
        for name, value in headers.items():
            message[name] = value
        super().__init__(body, message, url, code)
        self.msg = http.client.responses.get(code, "")  # Read by HTTPErrorProcessor.


@contextlib.contextmanager
def _serve(
    code: int, body: bytes | io.BytesIO = b"", headers: Mapping[str, str] | None = None
) -> Iterator[list[urllib.request.Request]]:
    """Answers HTTPS requests with one canned response and records each request.

    Only `HTTPSHandler.https_open` is replaced, so redirect handling (including
    `login_command._NoRedirect`) and error processing run for real.

    Args:
        code: HTTP status to return.
        body: Response body.
        headers: Response headers.

    Yields:
        The list of requests sent, in order.
    """
    requests: list[urllib.request.Request] = []

    def https_open(handler: object, req: urllib.request.Request) -> _FakeResponse:
        del handler
        requests.append(req)
        fp = io.BytesIO(body) if isinstance(body, bytes) else body
        return _FakeResponse(req.full_url, code, fp, headers or {})

    with mock.patch.object(
        urllib.request.HTTPSHandler, "https_open", autospec=True, side_effect=https_open
    ):
        yield requests


@contextlib.contextmanager
def _serve_http(code: int, body: bytes = b"") -> Iterator[list[urllib.request.Request]]:
    """Like `_serve`, but answers plain-http requests via `HTTPHandler.http_open`.

    A proxied request reaches `http_open` with the proxy as its host, so the
    recorded `host` shows whether an ambient proxy was used.

    Args:
        code: HTTP status to return.
        body: Response body, served fresh for each request.

    Yields:
        The list of requests sent, in order.
    """
    requests: list[urllib.request.Request] = []

    def http_open(handler: object, req: urllib.request.Request) -> _FakeResponse:
        del handler
        requests.append(req)
        return _FakeResponse(req.full_url, code, io.BytesIO(body), {})

    with mock.patch.object(
        urllib.request.HTTPHandler, "http_open", autospec=True, side_effect=http_open
    ):
        yield requests


@contextlib.contextmanager
def _dripping_server(give_up_after: float = 3.0) -> Iterator[str]:
    """Accepts one connection on 127.0.0.1 and sends it one byte every 50 ms.

    Only a wall-clock deadline ends such a request. The server closes the
    connection after `give_up_after` seconds, so a regression fails, not hangs.

    Args:
        give_up_after: Seconds after which the server closes the connection.

    Yields:
        The `http://127.0.0.1:<port>` origin.
    """
    listener = socket.create_server(("127.0.0.1", 0))
    listener.settimeout(give_up_after)
    port, stop = listener.getsockname()[1], threading.Event()

    def drip() -> None:
        with listener, contextlib.suppress(OSError):
            conn, _ = listener.accept()
            with conn:
                end = time.monotonic() + give_up_after
                while not stop.wait(0.05) and time.monotonic() < end:
                    conn.sendall(b"x")

    thread = threading.Thread(target=drip, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{port}"
    finally:
        stop.set()
        thread.join()


def _now() -> int:
    return int(datetime.datetime.now(datetime.UTC).timestamp())


def _raw_status(port: int, target: str) -> int:
    """Sends `GET <target>` to the loopback server verbatim and returns the status.

    `http.client` sends the request target unparsed and never uses a proxy or
    follows redirects, so malformed targets reach the handler as written.

    Args:
        port: Loopback server port.
        target: Request target, e.g. `/callback?state=...`.

    Returns:
        The HTTP status code.
    """
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        # skip_host stops http.client from parsing an absolute-form target itself.
        conn.putrequest("GET", target, skip_host=True)
        conn.putheader("Host", f"127.0.0.1:{port}")
        conn.endheaders()
        return conn.getresponse().status
    finally:
        conn.close()


class _CallbackClient:
    """Plays the browser: sends loopback callbacks on a thread for `open_url`.

    A failed assertion inside a thread does not fail the test, so the thread only
    records status codes (or the exception it hit), and `join` hands them to the
    test thread, which asserts on them.
    """

    def __init__(self, targets: Callable[[str], list[str]]) -> None:
        self._targets = targets  # Maps the login URL's `state` to request targets.
        self._thread: threading.Thread | None = None
        self._statuses: list[int] = []
        self._error: BaseException | None = None
        self.login_urls: list[str] = []

    def open_url(self, login_url: str) -> bool:
        self.login_urls.append(login_url)
        qs = urllib.parse.parse_qs(urllib.parse.urlsplit(login_url).query)
        args = (int(qs["port"][0]), self._targets(qs["state"][0]))
        self._thread = threading.Thread(target=self._send, args=args, daemon=True)
        self._thread.start()
        return True

    def _send(self, port: int, targets: list[str]) -> None:
        try:
            for target in targets:
                self._statuses.append(_raw_status(port, target))
        except BaseException as exc:  # Re-raised on the test thread by join().
            self._error = exc

    def join(self) -> list[int]:
        """Waits for the client thread, re-raises its error, and returns the statuses."""
        if self._thread is None:
            raise AssertionError("open_url was never called")
        self._thread.join(timeout=10)
        if self._thread.is_alive():
            raise AssertionError("the callback client thread did not finish")
        if self._error is not None:
            raise self._error
        return self._statuses


class LoginCommandTest(unittest.TestCase):
    """Unit tests for `xmc login`."""

    def setUp(self) -> None:
        super().setUp()
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self._xdg = temp_dir.name

    def _env(self, **extra: str) -> dict[str, str]:
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_UI_URL": _SERVER, "DISPLAY": ":0"}
        env.update(extra)
        return env

    def test_run_login_help_lists_every_flag(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        code = login_command.run_login(["--help"], env=self._env(), out=out, err=err)

        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertTrue(out.getvalue().startswith("usage: xmc login "))
        for flag in ("--no-browser", "--timeout", "--status", "--print-token"):
            self.assertIn(flag, out.getvalue())
        self.assertIn("default: 300.0", out.getvalue())

    def test_status_and_print_token_are_mutually_exclusive(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        code = login_command.run_login(
            ["--status", "--print-token"], env=self._env(), out=out, err=err
        )

        self.assertEqual((code, out.getvalue()), (2, ""))
        self.assertIn("not allowed with argument", err.getvalue())

    def test_status_shows_out_of_range_expiry_without_crashing(self) -> None:
        env, out = self._env(), io.StringIO()
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 10**20}
        credentials.save_credentials(credentials.credentials_path(env), {_SERVER: entry})

        rc = login_command.run_login(["--status"], env=env, out=out, err=io.StringIO())

        self.assertEqual(rc, 0)
        self.assertIn(f"until Unix time {10**20}.", out.getvalue())

    def test_run_login_browser_flow_exchanges_the_verifier_for_the_loopback_challenge(
        self,
    ) -> None:
        client = _CallbackClient(lambda state: [f"/callback?state={state}&code=c-1"])
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        env, out = self._env(), io.StringIO()
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant) as exchange,
        ):
            rc = login_command.run_login(
                ["--timeout", "5"], env=env, out=out, err=io.StringIO(), open_url=client.open_url
            )

        self.assertEqual(client.join(), [302])
        self.assertEqual(rc, 0)
        server, code, verifier = exchange.call_args.args
        digest = hashlib.sha256(verifier.encode("ascii")).digest()
        self.assertEqual((server, code), (_SERVER, "c-1"))
        (login_url,) = client.login_urls
        challenge = urllib.parse.parse_qs(urllib.parse.urlsplit(login_url).query)
        self.assertEqual(
            challenge["code_challenge"],
            [base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")],
        )
        self.assertIn(f"Logged in to {_SERVER} as u@example.com", out.getvalue())
        cached = credentials.load_credentials(credentials.credentials_path(env))
        self.assertEqual(cached[_SERVER]["access_token"], "jwt")

    def test_await_loopback_code_flushes_url_before_waiting(self) -> None:
        out = mock.Mock(wraps=io.StringIO())

        with self.assertRaisesRegex(credentials.AuthError, "Timed out"):
            login_command.await_loopback_code(_SERVER, "c", lambda _: True, out, timeout=0.05)

        self.assertIn("Opening browser to sign in", out.write.call_args.args[0])
        out.flush.assert_called_once_with()

    def test_run_login_reports_save_failure_after_token_exchange(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant),
            mock.patch.object(
                credentials.tempfile, "mkstemp", side_effect=PermissionError("denied")
            ),
        ):
            rc = login_command.run_login(
                ["--no-browser"], env=self._env(), out=out, err=err, prompt=lambda _: "c"
            )

        self.assertEqual(rc, 1)
        self.assertIn("Error: Cannot write credentials file", err.getvalue())
        self.assertNotIn("Logged in", out.getvalue())

    def test_run_login_treats_eof_at_code_prompt_as_no_code(self) -> None:
        err = io.StringIO()

        def closed_stdin(_: str) -> str:
            raise EOFError

        with mock.patch.object(login_command, "_probe_cli_login"):
            rc = login_command.run_login(
                ["--no-browser"], env=self._env(), out=io.StringIO(), err=err, prompt=closed_stdin
            )

        self.assertEqual(rc, 1)
        self.assertEqual(err.getvalue(), "Error: No authorization code entered.\n")

    def test_run_login_rejects_unknown_flag(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        code = login_command.run_login(["--bogus"], env=self._env(), out=out, err=err)

        self.assertEqual((code, out.getvalue()), (2, ""))
        self.assertIn("unrecognized arguments: --bogus", err.getvalue())

    def test_run_login_fails_before_prompting_on_unreadable_cache(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True)
        path.write_text("{not json", encoding="utf-8")
        os.chmod(path, 0o600)
        prompt, err = mock.Mock(return_value="c"), io.StringIO()

        with mock.patch.object(login_command, "_probe_cli_login") as mock_probe:
            rc = login_command.run_login(
                ["--no-browser"], env=env, out=io.StringIO(), err=err, prompt=prompt
            )

        self.assertEqual(rc, 1)
        self.assertIn("Corrupt credentials file", err.getvalue())
        mock_probe.assert_not_called()
        prompt.assert_not_called()

    def test_interactive_login_flushes_url_before_prompt(self) -> None:
        out = mock.Mock(wraps=io.StringIO(), encoding="utf-8")
        flushed_before_prompt: list[bool] = []

        def prompt(_: str) -> str:
            flushed_before_prompt.append(out.flush.called)
            return "c"

        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=_GOOD_GRANT),
        ):
            login_command._interactive_login(_SERVER, True, 1.0, {}, mock.Mock(), prompt, out)

        self.assertEqual(flushed_before_prompt, [True])

    def test_pkce_pair(self) -> None:
        verifier, challenge = login_command.pkce_pair()
        self.assertGreaterEqual(len(verifier), 43)
        digest = hashlib.sha256(verifier.encode("ascii")).digest()
        expected = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
        self.assertEqual(challenge, expected)

    def test_run_login_rejects_non_positive_or_non_finite_timeout(self) -> None:
        for timeout in ("0", "-1", "nan", "inf"):
            with self.subTest(timeout=timeout):
                err = io.StringIO()
                rc = login_command.run_login(
                    ["--timeout", timeout], env=self._env(), out=io.StringIO(), err=err
                )

                self.assertEqual(rc, 2)
                self.assertTrue(err.getvalue().startswith("usage: xmc login "))
                self.assertIn(
                    "xmc login: error: argument --timeout: must be a positive finite number",
                    err.getvalue(),
                )

    def test_run_login_rejects_a_non_numeric_timeout(self) -> None:
        err = io.StringIO()

        rc = login_command.run_login(
            ["--timeout", "soon"], env=self._env(), out=io.StringIO(), err=err
        )

        self.assertEqual(rc, 2)
        self.assertIn(
            "xmc login: error: argument --timeout: invalid number: 'soon'", err.getvalue()
        )

    def test_browser_available(self) -> None:
        cases = [
            ({"DISPLAY": ":0"}, "linux", True),
            ({"WAYLAND_DISPLAY": "wayland-0"}, "linux", True),
            ({}, "linux", False),
            ({}, "darwin", True),
            ({}, "win32", True),
            ({"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 55 1.2.3.5 22"}, "linux", False),
            ({"DISPLAY": ":0", "SSH_CLIENT": "1.2.3.4 55 22"}, "linux", False),
            ({"SSH_TTY": "/dev/pts/0"}, "darwin", False),
        ]
        for env, platform, expected in cases:
            with (
                self.subTest(env=env, platform=platform),
                mock.patch.object(login_command.sys, "platform", platform),
            ):
                self.assertIs(login_command.browser_available(env), expected)

    def test_await_loopback_code_rejects_forged_callbacks_and_returns_the_code(self) -> None:
        def targets(state: str) -> list[str]:
            return [
                "/callback?state=wrong&code=bad",
                "/callback?state=%C3%BC&code=bad",  # Non-ASCII state.
                f"/other?state={state}&code=bad",
                "http://[bad/callback",  # urlsplit raises ValueError.
                f"/callback?state={state}&code=good-code",
            ]

        client = _CallbackClient(targets)

        try:
            code = login_command.await_loopback_code(
                _SERVER, "challenge-xyz", client.open_url, io.StringIO(), timeout=5.0
            )
        finally:
            statuses = client.join()  # Surfaces a client failure instead of a timeout.

        self.assertEqual(statuses, [400, 400, 400, 400, 302])
        self.assertEqual(code, "good-code")

    def test_await_loopback_code_reports_callback_errors(self) -> None:
        cases = [
            ("&error=denied", 302, "Authentication failed: denied"),
            ("", 400, "Callback did not include an authorization code"),
        ]
        for query, status, message in cases:

            def targets(state: str, query: str = query) -> list[str]:
                return [f"/callback?state={state}{query}"]

            client = _CallbackClient(targets)
            with (
                self.subTest(query=query),
                self.assertRaisesRegex(credentials.AuthError, message),
            ):
                login_command.await_loopback_code(
                    _SERVER, "challenge-xyz", client.open_url, io.StringIO(), timeout=5.0
                )
            self.assertEqual(client.join(), [status])

    def test_build_login_url(self) -> None:
        url = login_command.build_login_url(_SERVER, "state-1", "challenge-1")

        split = urllib.parse.urlsplit(url)
        self.assertEqual(f"{split.scheme}://{split.netloc}{split.path}", f"{_SERVER}/api/cli/login")
        self.assertEqual(
            urllib.parse.parse_qs(split.query),
            {
                "mode": ["manual"],
                "state": ["state-1"],
                "code_challenge": ["challenge-1"],
                "code_challenge_method": ["S256"],
            },
        )

    def test_build_login_url_loopback_mode(self) -> None:
        url = login_command.build_login_url(_SERVER, "state-1", "challenge-1", port=8080)

        self.assertEqual(
            urllib.parse.parse_qs(urllib.parse.urlsplit(url).query),
            {
                "port": ["8080"],
                "state": ["state-1"],
                "code_challenge": ["challenge-1"],
                "code_challenge_method": ["S256"],
            },
        )

    def test_exchange_code_posts_form_and_returns_stripped_grant(self) -> None:
        body = json.dumps({**_GOOD_GRANT, "access_token": " jwt-token\n", "email": " u@x.io "})
        with _serve(200, body.encode("utf-8")) as requests:
            grant = login_command.exchange_code(_SERVER, "code-1", "verifier-1")

        self.assertEqual(
            grant, {"access_token": "jwt-token", "email": "u@x.io", "expires_in": 3600}
        )
        (req,) = requests
        self.assertEqual((req.full_url, req.get_method()), (f"{_SERVER}/api/cli/token", "POST"))
        self.assertEqual(req.timeout, 30)
        self.assertEqual(req.get_header("Content-type"), "application/x-www-form-urlencoded")
        self.assertEqual(req.get_header("Accept"), "application/json")
        assert isinstance(req.data, bytes)
        self.assertEqual(
            urllib.parse.parse_qs(req.data.decode("ascii")),
            {
                "grant_type": ["authorization_code"],
                "code": ["code-1"],
                "code_verifier": ["verifier-1"],
            },
        )

    def test_exchange_code_does_not_follow_redirects(self) -> None:
        # Following a 307/308 would replay the code and PKCE verifier to the target.
        for code in (301, 302, 303, 307, 308):
            headers = {"Location": "https://attacker.example.com/steal"}
            with (
                self.subTest(code=code),
                _serve(code, headers=headers) as requests,
                self.assertRaisesRegex(
                    credentials.AuthError, rf"^Token exchange failed: HTTP {code}\.$"
                ),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")
            self.assertEqual([r.full_url for r in requests], [f"{_SERVER}/api/cli/token"])

    def test_exchange_code_formats_http_error_bodies(self) -> None:
        cases: list[tuple[bytes | io.BytesIO, str]] = [
            (b'{"error_description": " Code expired "}', r"Code expired \(HTTP 400\)\.$"),
            (b'{"error": "invalid_grant"}', r"invalid_grant \(HTTP 400\)\.$"),
            (b'{"error": "  "}', r"failed: HTTP 400\.$"),
            (b"not json", r"failed: HTTP 400\.$"),
            (b"[]", r"failed: HTTP 400\.$"),
            (b"\xff\xfe", r"failed: HTTP 400\.$"),
            (b"[" * 4096, r"failed: HTTP 400\.$"),  # RecursionError while parsing.
            (_FailingBody(OSError("closed")), r"failed: HTTP 400\.$"),
            (_FailingBody(http.client.IncompleteRead(b"{")), r"failed: HTTP 400\.$"),
        ]
        for body, pattern in cases:
            payload = io.BytesIO(body) if isinstance(body, bytes) else body
            with (
                self.subTest(body=body),
                _serve(400, payload),
                self.assertRaisesRegex(credentials.AuthError, pattern),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")
            self.assertTrue(payload.closed)

    def test_exchange_code_rejects_malformed_grants(self) -> None:
        bodies: list[object] = [
            [],
            None,
            "x",
            {**_GOOD_GRANT, "token_type": "mac"},
            {k: v for k, v in _GOOD_GRANT.items() if k != "token_type"},
            {**_GOOD_GRANT, "access_token": ""},
            {**_GOOD_GRANT, "access_token": "  "},
            {**_GOOD_GRANT, "access_token": 7},
            {**_GOOD_GRANT, "email": ""},
            {**_GOOD_GRANT, "email": " \t"},
            {**_GOOD_GRANT, "email": None},
            {**_GOOD_GRANT, "expires_in": True},
            {**_GOOD_GRANT, "expires_in": 0},
            {**_GOOD_GRANT, "expires_in": -1},
            {**_GOOD_GRANT, "expires_in": 1.5},
            {**_GOOD_GRANT, "expires_in": "3600"},
            {**_GOOD_GRANT, "token_type": 7},
        ]
        for body in bodies:
            with (
                self.subTest(body=body),
                _serve(200, json.dumps(body).encode("utf-8")),
                self.assertRaisesRegex(credentials.AuthError, "Malformed token response"),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_rejects_tokens_and_emails_with_unsafe_characters(self) -> None:
        # A CR/LF or space in the token would inject headers wherever it is sent;
        # control or bidi characters in the email would reach the terminal.
        for field, value in (
            ("access_token", "jwt\r\nX-Injected: 1"),
            ("access_token", "jwt extra"),
            ("access_token", "jwt\x00"),
            ("access_token", "jwt\u00e9"),
            ("email", "u\x1b[2J@example.com"),
            ("email", "\u202eu@example.com"),
            ("email", "u\ud800@example.com"),
        ):
            body = json.dumps({**_GOOD_GRANT, field: value}).encode("utf-8")
            with (
                self.subTest(field=field, value=ascii(value)),
                _serve(200, body),
                self.assertRaisesRegex(credentials.AuthError, "Malformed token response"),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_bounds_expires_in(self) -> None:
        limit = login_command._MAX_EXPIRES_IN_SECONDS
        skew = login_command._EXPIRY_SKEW_SECONDS
        for accepted in (skew + 1, limit):
            body = json.dumps({**_GOOD_GRANT, "expires_in": accepted}).encode("utf-8")
            with self.subTest(accepted=accepted), _serve(200, body):
                grant = login_command.exchange_code(_SERVER, "code-1", "verifier-1")
                self.assertEqual(grant["expires_in"], accepted)
        # 10**4300 - 1 still parses, but `now + expires_in` could not be written.
        for expires_in in (limit + 1, 10**400, 10**4300 - 1):
            body = json.dumps({**_GOOD_GRANT, "expires_in": expires_in}).encode("utf-8")
            with (
                self.subTest(digits=len(str(expires_in))),
                _serve(200, body),
                self.assertRaisesRegex(credentials.AuthError, f"exceeds {limit} seconds"),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_rejects_lifetimes_within_the_xdash_expiry_skew(self) -> None:
        # xdash.auth treats a token as expired 30 s early, so it could never use these.
        for expires_in in (1, 29, login_command._EXPIRY_SKEW_SECONDS):
            body = json.dumps({**_GOOD_GRANT, "expires_in": expires_in}).encode("utf-8")
            with (
                self.subTest(expires_in=expires_in),
                _serve(200, body),
                self.assertRaisesRegex(credentials.AuthError, "^Token from server is already exp"),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_reads_at_most_4_kib_of_an_error_body(self) -> None:
        padding = b"x" * login_command._MAX_ERROR_BODY_BYTES
        body = b'{"pad": "' + padding + b'", "error": "invalid_grant"}'

        with (
            _serve(400, body),
            self.assertRaisesRegex(credentials.AuthError, r"^Token exchange failed: HTTP 400\.$"),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_rejects_unparseable_responses(self) -> None:
        bodies: list[tuple[str, bytes | io.BytesIO, str]] = [
            ("html", b"<html>", "Expecting value"),
            ("not utf-8", b"\xff\xfe", "codec can't decode"),
            ("deep nesting", b"[" * 65536, "recursion"),
            ("truncated", _FailingBody(http.client.IncompleteRead(b"{")), "IncompleteRead"),
            ("reset", _FailingBody(OSError("reset by peer")), "reset by peer"),
            ("oversized", b" " * (65536 + 1), "response too large"),
        ]
        for name, body, detail in bodies:
            with (
                self.subTest(name),
                _serve(200, body),
                self.assertRaisesRegex(
                    credentials.AuthError, f"^Token exchange failed: .*{detail}"
                ),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_wraps_transport_and_url_errors(self) -> None:
        errors: list[BaseException] = [
            OSError("reset"),
            urllib.error.URLError("refused"),
            TimeoutError("timed out"),
            http.client.BadStatusLine("garbage"),
            http.client.RemoteDisconnected("closed"),
        ]
        for exc in errors:
            with (
                self.subTest(exc=exc),
                mock.patch.object(urllib.request.HTTPSHandler, "https_open", side_effect=exc),
                self.assertRaisesRegex(credentials.AuthError, "^Token exchange failed: "),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        # ValueError from Request() and from IDNA encoding of the host.
        for server in ("not-a-url", "https://\u00fc..example.com"):
            with (
                self.subTest(server=server),
                self.assertRaisesRegex(credentials.AuthError, "^Token exchange failed: "),
            ):
                login_command.exchange_code(server, "code-1", "verifier-1")

    def test_probe_cli_login_accepts_http_400(self) -> None:
        buf = io.BytesIO(b"")
        with _serve(400, buf) as requests:
            login_command._probe_cli_login(_SERVER)

        self.assertTrue(buf.closed)
        (req,) = requests
        self.assertEqual((req.full_url, req.get_method()), (f"{_SERVER}/api/cli/login", "GET"))
        self.assertEqual(req.timeout, 10)

    def test_await_loopback_code_times_out_or_reports_unopenable_browser(self) -> None:
        with self.assertRaisesRegex(credentials.AuthError, "Timed out"):
            login_command.await_loopback_code(
                _SERVER, "challenge", lambda _: True, io.StringIO(), timeout=0.05
            )

        def broken_browser(_: str) -> bool:
            raise RuntimeError("no browser")

        self.assertIsNone(
            login_command.await_loopback_code(
                _SERVER, "challenge", broken_browser, io.StringIO(), timeout=5.0
            )
        )
        with mock.patch.object(
            login_command.http.server, "HTTPServer", side_effect=OSError("no IPv4 loopback")
        ):
            self.assertIsNone(
                login_command.await_loopback_code(
                    _SERVER, "challenge", lambda _: True, io.StringIO(), timeout=5.0
                )
            )

    def test_probe_cli_login_rejects_other_statuses(self) -> None:
        cases = [
            *((code, "requires XMC >= 0.16.0") for code in (401, 404, 405, 501)),
            *((code, f"unexpected HTTP {code}") for code in (200, 204)),
            *((code, f"returned HTTP {code} on") for code in (301, 302, 307, 308, 500, 502)),
        ]
        for code, pattern in cases:
            buf = io.BytesIO(b"<html>")
            headers = {"Location": "https://attacker.example.com/"} if 300 <= code < 400 else {}
            with (
                self.subTest(code=code),
                _serve(code, buf, headers) as requests,
                self.assertRaisesRegex(credentials.AuthError, pattern),
            ):
                login_command._probe_cli_login(_SERVER)
            self.assertTrue(buf.closed)
            self.assertEqual(len(requests), 1)  # Redirects are never followed.

    def test_probe_cli_login_wraps_transport_and_url_errors(self) -> None:
        for exc in (OSError("down"), http.client.BadStatusLine("garbage")):
            with (
                self.subTest(exc=exc),
                mock.patch.object(urllib.request.HTTPSHandler, "https_open", side_effect=exc),
                self.assertRaisesRegex(credentials.AuthError, f"Cannot reach .*: {exc}"),
            ):
                login_command._probe_cli_login(_SERVER)
        with self.assertRaisesRegex(credentials.AuthError, "Cannot reach"):
            login_command._probe_cli_login("https://\u00fc..example.com")  # Not IDNA-encodable.

    def test_format_expiry(self) -> None:
        self.assertRegex(login_command._format_expiry(0), r"^19(69|70)-\d\d-\d\d \d\d:\d\d ")
        self.assertEqual(login_command._format_expiry(2**62), f"Unix time {2**62}")

    def test_format_expiry_falls_back_when_local_conversion_fails(self) -> None:
        for exc in (OverflowError("too big"), OSError("bad tz"), ValueError("year 0")):
            with self.subTest(exc=exc), mock.patch.object(login_command, "datetime") as fake:
                fake.datetime.fromtimestamp.return_value.astimezone.side_effect = exc
                self.assertEqual(login_command._format_expiry(123), "Unix time 123")

    def test_run_login_manual_flow_caches_the_grant(self) -> None:
        out, err, env = io.StringIO(), io.StringIO(), self._env()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        before = _now()
        with (
            mock.patch.object(login_command, "_probe_cli_login") as mock_probe,
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            rc = login_command.run_login(
                ["--no-browser"], env=env, out=out, err=err, prompt=lambda _: "c"
            )
        mock_probe.assert_called_once_with(_SERVER)
        self.assertEqual((rc, err.getvalue()), (0, ""))
        self.assertIn(f"Logged in to {_SERVER} as u@example.com", out.getvalue())
        cached = credentials.load_credentials(credentials.credentials_path(env))
        self.assertEqual(list(cached), [_SERVER])
        self.assertEqual(cached[_SERVER]["access_token"], "jwt")
        self.assertEqual(cached[_SERVER]["email"], "u@example.com")
        expires_at = cached[_SERVER]["expires_at"]
        self.assertIsInstance(expires_at, int)
        self.assertTrue(before + 900 <= expires_at <= _now() + 900)

    def test_logout_clears_a_login_written_by_run_login(self) -> None:
        env = self._env()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            login_command.run_login(
                [], env=env, out=io.StringIO(), err=io.StringIO(), prompt=lambda _: "c"
            )
        out, err = io.StringIO(), io.StringIO()

        rc = logout_command.run_logout([], env=env, out=out, err=err)

        self.assertEqual(
            (rc, out.getvalue(), err.getvalue()), (0, f"Logged out of {_SERVER}.\n", "")
        )
        self.assertEqual(credentials.load_credentials(credentials.credentials_path(env)), {})

    def test_run_login_probes_before_prompting(self) -> None:
        env, err, prompt = self._env(), io.StringIO(), mock.Mock(return_value="c")
        with _serve(404):
            rc = login_command.run_login([], env=env, out=io.StringIO(), err=err, prompt=prompt)

        self.assertEqual(rc, 1)
        self.assertRegex(err.getvalue(), r"^Error: Server .* does not support `xmc login`")
        prompt.assert_not_called()
        self.assertFalse(credentials.credentials_path(env).exists())

    def test_run_login_binds_pkce_challenge_and_strips_the_pasted_code(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=_GOOD_GRANT) as exchange,
        ):
            rc = login_command.run_login(
                [], env=self._env(), out=out, err=err, prompt=lambda _: "  code-1 \n"
            )

        self.assertEqual((rc, err.getvalue()), (0, ""))
        (server, code, verifier), _ = exchange.call_args
        self.assertEqual((server, code), (_SERVER, "code-1"))
        url = next(word for word in out.getvalue().split() if "/api/cli/login?" in word)
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(url).query)
        digest = hashlib.sha256(verifier.encode("ascii")).digest()
        expected_challenge = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
        self.assertEqual(query["code_challenge"], [expected_challenge])
        self.assertEqual((query["code_challenge_method"], query["mode"]), (["S256"], ["manual"]))

    def test_read_code_rejects_blank_and_unsafe_codes(self) -> None:
        cases = [
            ("   \t\n", "No authorization code entered"),
            ("code 1", "Invalid authorization code"),
            ("code\x1b[2J", "Invalid authorization code"),
            ("code\udcff", "Invalid authorization code"),  # Undecodable terminal byte.
            ("cöde", "Invalid authorization code"),
        ]
        for typed, message in cases:
            with (
                self.subTest(typed=ascii(typed)),
                self.assertRaisesRegex(credentials.AuthError, message),
            ):
                login_command._read_code(mock.Mock(return_value=typed))

    def test_run_login_reports_prompt_failures(self) -> None:
        errors: list[Exception] = [
            ValueError("I/O operation on closed file"),
            UnicodeDecodeError("utf-8", b"\xff", 0, 1, "invalid start byte"),
            OSError(5, "Input/output error"),
            AttributeError("'NoneType' object has no attribute 'readline'"),  # stdin is None.
            *(error_class("tcsetattr") for error_class in login_command._TTY_ERRORS),
        ]
        for exc in errors:
            err = io.StringIO()
            with (
                self.subTest(exc=type(exc).__name__),
                mock.patch.object(login_command, "_probe_cli_login"),
            ):
                rc = login_command.run_login(
                    [],
                    env=self._env(),
                    out=io.StringIO(),
                    err=err,
                    prompt=mock.Mock(side_effect=exc),
                )
                self.assertEqual(rc, 1)
                self.assertTrue(
                    err.getvalue().startswith("Error: Cannot read the authorization code: ")
                )

    def test_run_login_defaults_to_process_env_and_streams(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with (
            mock.patch.dict(os.environ, {"XDG_CONFIG_HOME": self._xdg}, clear=True),
            mock.patch.object(sys, "stdout", out),
            mock.patch.object(sys, "stderr", err),
        ):
            self.assertEqual(login_command.run_login([]), 1)

        self.assertEqual(out.getvalue(), "")
        self.assertEqual(err.getvalue(), f"Error: {credentials.NO_SERVER_MESSAGE}\n")

    def test_run_login_escapes_server_error_text_on_a_strict_stream(self) -> None:
        # JSON can carry ESC and lone surrogates; a real UTF-8 stderr must not crash.
        body = b'{"error_description": "\\u001b[2Jbad\\ud800code"}'
        err = io.TextIOWrapper(io.BytesIO(), encoding="utf-8", errors="strict")
        with mock.patch.object(login_command, "_probe_cli_login"), _serve(400, body):
            rc = login_command.run_login(
                [], env=self._env(), out=io.StringIO(), err=err, prompt=lambda _: "c"
            )

        err.flush()
        self.assertEqual(rc, 1)
        self.assertIn(b"\\x1b[2Jbad\\ud800code (HTTP 400)", err.buffer.getvalue())

    def test_exchange_code_rejects_an_unencodable_code_without_sending_it(self) -> None:
        with (
            _serve(200, json.dumps(_GOOD_GRANT).encode("utf-8")) as requests,
            self.assertRaisesRegex(credentials.AuthError, "not valid text"),
        ):
            login_command.exchange_code(_SERVER, "code\udcff", "verifier-1")

        self.assertEqual(requests, [])

    def test_loopback_requests_bypass_ambient_proxies(self) -> None:
        # Plain http:// is allowed only for loopback, so a proxy would carry the code
        # and PKCE verifier off the machine in cleartext.
        proxies = {"http_proxy": "http://proxy.invalid:3128", "no_proxy": ""}
        proxies["https_proxy"] = proxies["HTTP_PROXY"] = proxies["http_proxy"]
        for host in ("localhost", "127.0.0.1", "[::1]"):
            server = f"http://{host}:8080"
            with self.subTest(host=host), mock.patch.dict(os.environ, proxies):
                with _serve_http(400) as probe_requests:
                    login_command._probe_cli_login(server)
                with _serve_http(200, json.dumps(_GOOD_GRANT).encode()) as token_requests:
                    grant = login_command.exchange_code(server, "code-1", "verifier-1")

                self.assertEqual([r.host for r in probe_requests], [f"{host}:8080"])
                self.assertEqual([r.host for r in token_requests], [f"{host}:8080"])
                self.assertEqual(grant["access_token"], _GOOD_GRANT["access_token"])

    def test_exchange_code_gives_up_on_a_dripping_server_at_the_deadline(self) -> None:
        with (
            mock.patch.object(login_command, "_TOKEN_TIMEOUT_SECONDS", 0.3),
            _dripping_server() as server,
            self.assertRaisesRegex(
                credentials.AuthError,
                r"^Token exchange failed: no complete response within 0\.3 seconds\.$",
            ),
        ):
            start = time.monotonic()
            login_command.exchange_code(server, "code-1", "verifier-1")

        self.assertLess(time.monotonic() - start, 2)

    def test_probe_gives_up_on_a_dripping_server_at_the_deadline(self) -> None:
        with (
            mock.patch.object(login_command, "_PROBE_TIMEOUT_SECONDS", 0.3),
            _dripping_server() as server,
            self.assertRaisesRegex(
                credentials.AuthError, r"^Cannot reach .*: no complete response within 0\.3 seconds"
            ),
        ):
            start = time.monotonic()
            login_command._probe_cli_login(server)

        self.assertLess(time.monotonic() - start, 2)

    def test_remote_requests_keep_ambient_proxies(self) -> None:
        proxy_env = {"https_proxy": "http://proxy.invalid:3128", "no_proxy": ""}
        with mock.patch.dict(os.environ, proxy_env), _serve(400) as requests:
            login_command._probe_cli_login(_SERVER)

        (req,) = requests
        self.assertEqual(req.host, "proxy.invalid:3128")

    def test_exchange_code_and_probe_reject_disallowed_url_schemes(self) -> None:
        for server in (
            "file:///etc/passwd",
            "ftp://example.com",
            "data:text/plain,hello",
            "http://xmc.example.com",
        ):
            with (
                self.subTest(server=server),
                self.assertRaisesRegex(
                    credentials.AuthError,
                    "^Token exchange failed: unsupported deployment URL scheme",
                ),
            ):
                login_command.exchange_code(server, "code-1", "verifier-1")
            with (
                self.subTest(probe_server=server),
                self.assertRaisesRegex(
                    credentials.AuthError, "^Cannot reach .*: unsupported deployment URL scheme"
                ),
            ):
                login_command._probe_cli_login(server)

    def test_exchange_code_and_probe_reject_invalid_origins_without_leaking_secrets(self) -> None:
        for server in (
            "https://",
            "http://user:s3cret@xmc.example.com",
            "https://user:s3cret@xmc.example.com",
            "https://xmc.example.com/extra?q=1",
            "https://xmc.example.com:0",
            "https://xmc.example.com#frag",
        ):
            with self.subTest(server=server):
                with self.assertRaisesRegex(
                    credentials.AuthError, "^Token exchange failed: invalid deployment URL origin$"
                ) as exc_ctx:
                    login_command.exchange_code(server, "code-1", "verifier-1")
                self.assertNotIn("s3cret", str(exc_ctx.exception))
            with self.subTest(probe_server=server):
                with self.assertRaisesRegex(
                    credentials.AuthError, "^Cannot reach .*: invalid deployment URL origin$"
                ) as probe_ctx:
                    login_command._probe_cli_login(server)
                self.assertNotIn("s3cret", str(probe_ctx.exception))

    def test_run_login_chooses_manual_or_browser_flow(self) -> None:
        ssh = {"SSH_CONNECTION": "1.2.3.4 55 1.2.3.5 22"}
        no_open = mock.Mock(side_effect=AssertionError("unexpected open_url"))
        cases: list[tuple[list[str], dict[str, str], Callable[[str], bool], str | None]] = [
            (["--no-browser"], self._env(), no_open, None),
            ([], self._env(**ssh), no_open, "No local browser detected; using manual login."),
            ([], self._env(), lambda _: False, "Could not launch browser; falling back"),
        ]
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        for argv, env, open_url, notice in cases:
            out, err = io.StringIO(), io.StringIO()
            with (
                self.subTest(argv=argv, env=env),
                mock.patch.object(login_command, "_probe_cli_login"),
                mock.patch.object(login_command, "exchange_code", return_value=grant),
            ):
                rc = login_command.run_login(
                    argv, env=env, out=out, err=err, open_url=open_url, prompt=lambda _: "c"
                )
                self.assertEqual((rc, err.getvalue()), (0, ""))
                self.assertIn("mode=manual", out.getvalue())
                if notice:
                    self.assertIn(notice, out.getvalue())
                else:
                    self.assertNotIn("manual login", out.getvalue())

    def test_status_and_print_token_report_the_cached_login(self) -> None:
        env = self._env()
        entry = {"access_token": " jwt ", "email": "u@example.com", "expires_at": _now() + 900}
        credentials.save_credentials(credentials.credentials_path(env), {_SERVER: entry})
        status_out, token_out, err = io.StringIO(), io.StringIO(), io.StringIO()

        status_rc = login_command.run_login(["--status"], env=env, out=status_out, err=err)
        token_rc = login_command.run_login(["--print-token"], env=env, out=token_out, err=err)

        self.assertEqual((status_rc, token_rc, err.getvalue()), (0, 0, ""))
        self.assertIn(f"Logged in to {_SERVER} as u@example.com until", status_out.getvalue())
        self.assertNotIn("jwt", status_out.getvalue())
        self.assertEqual(token_out.getvalue(), "jwt\n")

    def test_valid_entry_and_loopback_handler_timeout(self) -> None:
        self.assertEqual(login_command._callback_handler(_SERVER, "s", {}).timeout, 10.0)
        huge_entry = {"access_token": "jwt", "email": "u@x.io", "expires_at": 10**400}
        self.assertEqual(
            login_command._valid_entry(huge_entry),
            {"access_token": "jwt", "email": "u@x.io", "expires_at": 10**400},
        )

    def test_expired_or_malformed_entries_are_not_logged_in(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        now = _now()
        good = {"access_token": "old", "email": "u@x.io", "expires_at": now + 900}
        for bad_entry in (
            {**good, "expires_at": now - 10},
            {**good, "expires_at": now + 30},  # Inside the 60-second expiry skew.
            {**good, "expires_at": "not-an-int"},
            {**good, "expires_at": True},
            {**good, "expires_at": float("nan")},
            {**good, "expires_at": float("inf")},
            {**good, "access_token": "  "},
            {**good, "email": " "},
            {k: v for k, v in good.items() if k != "email"},
            "not-a-dict",
        ):
            with self.subTest(entry=bad_entry):
                credentials.save_credentials(path, {_SERVER: bad_entry})  # type: ignore[dict-item]
                token_out, token_err = io.StringIO(), io.StringIO()
                rc = login_command.run_login(
                    ["--print-token"], env=env, out=token_out, err=token_err
                )
                self.assertEqual((rc, token_out.getvalue()), (1, ""))
                self.assertIn(f"No valid login for {_SERVER}", token_err.getvalue())
                status_out, status_err = io.StringIO(), io.StringIO()
                rc = login_command.run_login(["--status"], env=env, out=status_out, err=status_err)
                self.assertEqual((rc, status_out.getvalue()), (1, ""))
                self.assertIn(f"Not logged in to {_SERVER}", status_err.getvalue())

    def test_logout_all_clears_every_cached_login(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        entry = {"access_token": "t", "email": "u@x.io", "expires_at": _now() + 900}
        credentials.save_credentials(path, {_SERVER: entry, "https://b.example.com": entry})

        rc = logout_command.run_logout(["--all"], env=env, out=io.StringIO(), err=io.StringIO())

        self.assertEqual(rc, 0)
        self.assertEqual(credentials.load_credentials(path), {})

    def test_run_login_preserves_entries_written_during_interactive_login(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        other = {"access_token": "o", "email": "o@example.com", "expires_at": 1}
        credentials.save_credentials(path, {"https://stale.example.com": other})
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        err = io.StringIO()

        def prompt(_: str) -> str:
            # A concurrent `xmc logout`/`login` rewrites the cache while we wait.
            credentials.save_credentials(path, {"https://other.example.com": other})
            return "c"

        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            rc = login_command.run_login(
                ["--no-browser"], env=env, out=io.StringIO(), err=err, prompt=prompt
            )

        self.assertEqual((rc, err.getvalue()), (0, ""))
        self.assertEqual(
            sorted(credentials.load_credentials(path)), ["https://other.example.com", _SERVER]
        )


if __name__ == "__main__":
    unittest.main()
