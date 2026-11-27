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
import tempfile
import unittest
import urllib.error
import urllib.parse
import urllib.request
import urllib.response
from collections.abc import Iterator, Mapping
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
    fp = body if isinstance(body, io.BytesIO) else io.BytesIO(body)

    def https_open(handler: object, req: urllib.request.Request) -> _FakeResponse:
        del handler
        requests.append(req)
        return _FakeResponse(req.full_url, code, fp, headers or {})

    with mock.patch.object(
        urllib.request.HTTPSHandler, "https_open", autospec=True, side_effect=https_open
    ):
        yield requests


def _now() -> int:
    return int(datetime.datetime.now(datetime.UTC).timestamp())


class LoginCommandTest(unittest.TestCase):
    """Unit tests for `xmc login`."""

    def setUp(self) -> None:
        super().setUp()
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self._xdg = temp_dir.name

    def _env(self, **extra: str) -> dict[str, str]:
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_UI_URL": _SERVER}
        env.update(extra)
        return env

    def test_run_login_help_returns_zero_without_exiting(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        code = login_command.run_login(["--help"], env=self._env(), out=out, err=err)
        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertTrue(out.getvalue().startswith("usage: xmc login "))

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
                [], env=self._env(), out=out, err=err, prompt=lambda _: "c"
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
                [], env=self._env(), out=io.StringIO(), err=err, prompt=closed_stdin
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
            rc = login_command.run_login([], env=env, out=io.StringIO(), err=err, prompt=prompt)

        self.assertEqual(rc, 1)
        self.assertIn("Corrupt credentials file", err.getvalue())
        mock_probe.assert_not_called()
        prompt.assert_not_called()

    def test_interactive_login_flushes_url_before_prompt(self) -> None:
        out = mock.Mock(wraps=io.StringIO())
        flushed_before_prompt: list[bool] = []

        def prompt(_: str) -> str:
            flushed_before_prompt.append(out.flush.called)
            return "c"

        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=_GOOD_GRANT),
        ):
            login_command._interactive_login(_SERVER, prompt, out)

        self.assertEqual(flushed_before_prompt, [True])

    def test_pkce_pair(self) -> None:
        verifier, challenge = login_command.pkce_pair()
        self.assertGreaterEqual(len(verifier), 43)
        digest = hashlib.sha256(verifier.encode("ascii")).digest()
        expected = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
        self.assertEqual(challenge, expected)

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
            with (
                self.subTest(body=body),
                _serve(400, body),
                self.assertRaisesRegex(credentials.AuthError, pattern),
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

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
        ]
        for body in bodies:
            with (
                self.subTest(body=body),
                _serve(200, json.dumps(body).encode("utf-8")),
                self.assertRaisesRegex(credentials.AuthError, "Malformed token response"),
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
        with _serve(400) as requests:
            login_command._probe_cli_login(_SERVER)

        (req,) = requests
        self.assertEqual((req.full_url, req.get_method()), (f"{_SERVER}/api/cli/login", "GET"))
        self.assertEqual(req.timeout, 10)

    def test_probe_cli_login_rejects_other_statuses(self) -> None:
        cases = [
            *((code, "requires XMC >= 0.16.0") for code in (401, 404, 405, 501)),
            *((code, f"unexpected HTTP {code}") for code in (200, 204)),
            *((code, f"returned HTTP {code} on") for code in (301, 302, 307, 308, 500, 502)),
        ]
        for code, pattern in cases:
            headers = {"Location": "https://attacker.example.com/"} if 300 <= code < 400 else {}
            with (
                self.subTest(code=code),
                _serve(code, b"<html>", headers) as requests,
                self.assertRaisesRegex(credentials.AuthError, pattern),
            ):
                login_command._probe_cli_login(_SERVER)
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

    def test_run_login_manual_flow_caches_grant_and_logout_clears_it(self) -> None:
        out, err, env = io.StringIO(), io.StringIO(), self._env()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        before = _now()
        with (
            mock.patch.object(login_command, "_probe_cli_login") as mock_probe,
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            rc = login_command.run_login([], env=env, out=out, err=err, prompt=lambda _: "c")
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
        self.assertEqual(logout_command.run_logout([], env=env, out=out, err=err), 0)
        self.assertEqual(credentials.load_credentials(credentials.credentials_path(env)), {})

    def test_run_login_preserves_entries_written_during_interactive_login(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        other = {"access_token": "o", "email": "o@example.com", "expires_at": 1}
        credentials.save_credentials(path, {"https://stale.example.com": other})
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}

        def prompt(_: str) -> str:
            # A concurrent `xmc logout`/`login` rewrites the cache while we wait.
            credentials.save_credentials(path, {"https://other.example.com": other})
            return "c"

        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            rc = login_command.run_login([], env=env, out=io.StringIO(), prompt=prompt)

        self.assertEqual(rc, 0)
        self.assertEqual(
            sorted(credentials.load_credentials(path)), ["https://other.example.com", _SERVER]
        )


if __name__ == "__main__":
    unittest.main()
