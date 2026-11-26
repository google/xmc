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

import email.message
import http.client
import io
import json
import os
import urllib.error
import urllib.request
from unittest import mock

from absl.testing import absltest

from xm_cloud.cli import credentials, login_command, logout_command

_SERVER = "https://xmc.example.com"


# absl-py ships no type information to mypy here, so absltest.TestCase is typed as Any.
class LoginCommandTest(absltest.TestCase):  # type: ignore[misc]
    """Unit tests for `xmc login`."""

    def setUp(self) -> None:
        super().setUp()
        self._xdg = self.create_tempdir().full_path

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

    def test_pkce_pair(self) -> None:
        verifier, challenge = login_command.pkce_pair()
        self.assertGreaterEqual(len(verifier), 43)
        self.assertNotIn("=", challenge)

    def test_exchange_code_success_and_http_error(self) -> None:
        body = json.dumps(
            {
                "access_token": "jwt-token",
                "token_type": "Bearer",
                "expires_in": 3600,
                "email": "researcher@example.com",
            }
        ).encode("utf-8")
        # BytesIO is already a context manager that returns itself, like urlopen's response.
        fake_resp = io.BytesIO(body)
        with mock.patch.object(urllib.request, "urlopen", return_value=fake_resp):
            grant = login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        self.assertEqual(grant["access_token"], "jwt-token")
        http_err = urllib.error.HTTPError(
            f"{_SERVER}/api/cli/token",
            400,
            "Bad Request",
            email.message.Message(),
            io.BytesIO(b"{}"),
        )
        with mock.patch.object(urllib.request, "urlopen", side_effect=http_err):
            with self.assertRaisesRegex(
                credentials.AuthError, r"Token exchange failed: HTTP 400\."
            ):
                login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_formats_http_error_bodies(self) -> None:
        cases = [
            (b'{"error_description": "Code expired"}', "Code expired \\(HTTP 400\\)"),
            (b'{"error": "invalid_grant"}', "invalid_grant \\(HTTP 400\\)"),
            (b"not json", "HTTP 400"),
        ]
        for body, pattern in cases:
            with self.subTest(body=body):
                http_err = urllib.error.HTTPError(
                    f"{_SERVER}/api/cli/token",
                    400,
                    "Bad Request",
                    email.message.Message(),
                    io.BytesIO(body),
                )
                with (
                    mock.patch.object(urllib.request, "urlopen", side_effect=http_err),
                    self.assertRaisesRegex(credentials.AuthError, pattern),
                ):
                    login_command.exchange_code(_SERVER, "code-1", "verifier-1")

        unreadable = urllib.error.HTTPError(
            f"{_SERVER}/api/cli/token",
            502,
            "Bad Gateway",
            email.message.Message(),
            None,
        )
        with (
            mock.patch.object(unreadable, "read", side_effect=OSError("closed")),
            mock.patch.object(urllib.request, "urlopen", side_effect=unreadable),
            self.assertRaisesRegex(credentials.AuthError, r"Token exchange failed: HTTP 502\."),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_exchange_code_rejects_malformed_responses_and_network_errors(self) -> None:
        good = {
            "access_token": "jwt-token",
            "token_type": "Bearer",
            "expires_in": 3600,
            "email": "researcher@example.com",
        }
        bodies: list[object] = [
            [],
            None,
            "x",
            {**good, "token_type": "mac"},
            {**good, "expires_in": True},
            {**good, "expires_in": 0},
            {**good, "email": ""},
        ]
        for body in bodies:
            with self.subTest(body=body):
                resp = io.BytesIO(json.dumps(body).encode("utf-8"))
                with (
                    mock.patch.object(urllib.request, "urlopen", return_value=resp),
                    self.assertRaisesRegex(credentials.AuthError, "Malformed token response"),
                ):
                    login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        with (
            mock.patch.object(urllib.request, "urlopen", side_effect=OSError("reset")),
            self.assertRaisesRegex(credentials.AuthError, "Token exchange failed: reset"),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        with (
            mock.patch.object(
                urllib.request, "urlopen", side_effect=http.client.BadStatusLine("garbage")
            ),
            self.assertRaisesRegex(credentials.AuthError, "Token exchange failed: garbage"),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_probe_cli_login_reports_malformed_http_responses(self) -> None:
        with (
            mock.patch.object(
                urllib.request.OpenerDirector,
                "open",
                side_effect=http.client.BadStatusLine("garbage"),
            ),
            self.assertRaisesRegex(credentials.AuthError, "Cannot reach .*: garbage"),
        ):
            login_command._probe_cli_login(_SERVER)

    def test_probe_cli_login_supports_new_and_rejects_old_xmc(self) -> None:
        def _http_err(code: int) -> urllib.error.HTTPError:
            return urllib.error.HTTPError(
                f"{_SERVER}/api/cli/login",
                code,
                "err",
                email.message.Message(),
                io.BytesIO(b""),
            )

        with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=_http_err(400)):
            login_command._probe_cli_login(_SERVER)
        for old_code in (401, 404):
            with mock.patch.object(
                urllib.request.OpenerDirector, "open", side_effect=_http_err(old_code)
            ):
                with self.assertRaisesRegex(credentials.AuthError, "requires XMC >= 0.16.0"):
                    login_command._probe_cli_login(_SERVER)
        with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=_http_err(302)):
            with self.assertRaisesRegex(credentials.AuthError, "HTTP 302"):
                login_command._probe_cli_login(_SERVER)
        with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=OSError("down")):
            with self.assertRaisesRegex(credentials.AuthError, "Cannot reach"):
                login_command._probe_cli_login(_SERVER)
        with self.assertRaisesRegex(credentials.AuthError, "Cannot reach"):
            login_command._probe_cli_login("https://\u00fc..example.com")  # Not IDNA-encodable.

    def test_run_login_manual_flow_caches_grant_and_logout_clears_it(self) -> None:
        out, err, env = io.StringIO(), io.StringIO(), self._env()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        with (
            mock.patch.object(login_command, "_probe_cli_login") as mock_probe,
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            rc = login_command.run_login([], env=env, out=out, err=err, prompt=lambda _: "c")
        mock_probe.assert_called_once_with(_SERVER)
        self.assertEqual(rc, 0)
        self.assertIn(f"Logged in to {_SERVER} as u@example.com", out.getvalue())
        cached = credentials.load_credentials(credentials.credentials_path(env))
        self.assertEqual(cached[_SERVER]["access_token"], "jwt")
        self.assertEqual(logout_command.run_logout([], env=env, out=out, err=err), 0)
        self.assertEqual(credentials.load_credentials(credentials.credentials_path(env)), {})

    def test_login_command_exits_only_on_failure(self) -> None:
        # run_login binds os.environ as its default, so patching it in place reaches it.
        with mock.patch.dict(os.environ, self._env(), clear=True):
            with (
                mock.patch.object(
                    login_command, "_probe_cli_login", side_effect=credentials.AuthError("down")
                ),
                self.assertRaises(SystemExit) as login_exit,
            ):
                login_command.login_command(["xmanager", "login"])
        self.assertEqual(login_exit.exception.code, 1)


if __name__ == "__main__":
    absltest.main()
