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
import email.message
import hashlib
import http.client
import io
import json
import os
import pathlib
import stat
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from unittest import mock

from absl.testing import absltest
from xdash import auth as xdash_auth

from xm_cloud.cli import login_command

_SERVER = "https://xmc.example.com"


# absl-py ships no type information to mypy here, so absltest.TestCase is typed as Any.
class LoginCommandTest(absltest.TestCase):  # type: ignore[misc]
    """Unit tests for `xmc login` and `xmc logout`."""

    def setUp(self) -> None:
        super().setUp()
        self._xdg = self.create_tempdir().full_path

    def _env(self, **extra: str) -> dict[str, str]:
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_UI_URL": _SERVER}
        env["DISPLAY"] = ":0"
        env.update(extra)
        return env

    def test_resolve_server_precedence_and_validation(self) -> None:
        cases = [
            (
                {
                    "XMC_API_URL": "https://a.io/v1",
                    "XMANAGER_UI_URL": "https://b.io",
                },
                "https://a.io",
            ),
            ({"XMANAGER_UI_URL": "https://x.dev/ui"}, "https://x.dev"),
            ({"XMC_DOMAIN": "xmc.example.com"}, "https://xmc.example.com"),
            (
                {
                    "XMANAGER_ENDPOINT": "dns:///grpc.a.dev:443",
                    "XMC_API_URL": "https://b.dev",
                },
                "https://a.dev",
            ),
            (
                {
                    "XMANAGER_ENDPOINT": "localhost:50051",
                    "XMC_API_URL": "https://b.dev/v1",
                },
                "https://b.dev",
            ),
            ({"XMC_API_URL": "http://127.0.0.1:8080"}, "http://127.0.0.1:8080"),
        ]
        for env, expected in cases:
            self.assertEqual(login_command.resolve_server(env), expected)
        with self.assertRaises(login_command.AuthError) as ctx:
            login_command.resolve_server({})
        self.assertEqual(str(ctx.exception), login_command.NO_SERVER_MESSAGE)
        for bad_env in (
            {"XMC_API_URL": "http://xmc.example.com"},
            {"XMC_API_URL": "https://xmc.example.com?q=1"},
            {"XMC_API_URL": "https://[::1"},
            {"XMC_API_URL": "https://xmc.example.com:abc"},
            {"XMANAGER_ENDPOINT": "api.example.com:443", "XMC_API_URL": _SERVER},
            {"XMANAGER_ENDPOINT": "dns:///grpc."},
        ):
            with self.subTest(env=bad_env), self.assertRaises(login_command.AuthError):
                login_command.resolve_server(bad_env)

    def test_resolve_server_matches_the_xdash_cache_key(self) -> None:
        # xdash.auth looks up cached tokens by this origin, so the writer must agree.
        envs = [
            {"XMANAGER_ENDPOINT": "dns:///grpc.api.prod.example.com:443"},
            {"XMANAGER_ENDPOINT": "grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "https://grpc.x.dev"},
            {"XMANAGER_ENDPOINT": "https://grpc.x.dev:8443"},
            {"XMANAGER_ENDPOINT": "https://xmc.example.com"},
            {"XMANAGER_ENDPOINT": "http://localhost:8080"},
            {"XMANAGER_ENDPOINT": "localhost:50051", "XMC_API_URL": "https://b.dev/v1"},
            {"XMANAGER_ENDPOINT": "grpc://grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "dns://8.8.8.8/grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "grpc://localhost:50051", "XMC_API_URL": "https://b.dev"},
            {"XMC_API_URL": "https://XMC.example.com:443/v1"},
            {"XMC_API_URL": "https://xmc.example.com:8443"},
            {"XMC_API_URL": "https://grpc.api.x.dev"},
            {"XMC_API_URL": "http://[::1]:80"},
            {"XMANAGER_UI_URL": "https://grpc.x.dev/ui"},
            {"XMC_DOMAIN": "grpc.x.dev"},
            {"XMC_DOMAIN": "xmc.example.com:8443"},
        ]
        for env in envs:
            with self.subTest(env=env), mock.patch.dict(os.environ, env, clear=True):
                self.assertEqual(login_command.resolve_server(env), xdash_auth.resolve_xmc_origin())

    def test_credentials_path_matches_the_xdash_credentials_file(self) -> None:
        envs = [
            {},
            {"XMC_CREDENTIALS_FILE": os.path.join(self._xdg, "custom.json")},
            {"XMC_CREDENTIALS_FILE": "~/custom.json"},
            {"XMC_CREDENTIALS_FILE": "relative.json", "XDG_CONFIG_HOME": self._xdg},
            {"XDG_CONFIG_HOME": "~/cfg"},
            {"XDG_CONFIG_HOME": "relative"},
        ]
        for env in envs:
            with self.subTest(env=env), mock.patch.dict(os.environ, env, clear=True):
                self.assertEqual(
                    login_command.credentials_path(env), xdash_auth.get_credentials_file_path()
                )

    def test_resolve_server_drops_grpc_prefixes_and_default_ports(self) -> None:
        cases = [
            (
                {"XMANAGER_ENDPOINT": "dns:///grpc.api.prod.example.com:443"},
                "https://prod.example.com",
            ),
            ({"XMANAGER_ENDPOINT": "https://grpc.x.dev:8443"}, "https://x.dev:8443"),
            ({"XMANAGER_ENDPOINT": "http://localhost:8080"}, "http://localhost:8080"),
            ({"XMC_API_URL": "https://XMC.example.com:443/v1"}, "https://xmc.example.com"),
            ({"XMC_DOMAIN": "grpc.x.dev"}, "https://x.dev"),
        ]
        for env, expected in cases:
            with self.subTest(env=env):
                self.assertEqual(login_command.resolve_server(env), expected)
        for bad_env in (
            {"XMC_DOMAIN": "xmc.example.com/ui"},
            {"XMC_API_URL": "xmc.example.com"},
            {"XMANAGER_UI_URL": "xmc.example.com:8443"},
            {"XMC_API_URL": "https://0.0.0.0"},
            {"XMANAGER_ENDPOINT": "grpc.localhost:443"},
        ):
            with self.subTest(env=bad_env), self.assertRaises(login_command.AuthError):
                login_command.resolve_server(bad_env)

    def test_status_shows_out_of_range_expiry_without_crashing(self) -> None:
        env, out = self._env(), io.StringIO()
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 10**20}
        login_command.save_credentials(login_command.credentials_path(env), {_SERVER: entry})

        rc = login_command.run_login(["--status"], env=env, out=out, err=io.StringIO())

        self.assertEqual(rc, 0)
        self.assertIn(f"until Unix time {10**20}.", out.getvalue())

    def test_credentials_path_expands_user_in_xdg_config_home(self) -> None:
        self.assertEqual(
            login_command.credentials_path({"XDG_CONFIG_HOME": "~/cfg"}),
            pathlib.Path.home() / "cfg" / "xmc" / "credentials.json",
        )

    def test_credentials_path_reports_a_missing_home_directory(self) -> None:
        with (
            mock.patch.object(pathlib.Path, "home", side_effect=RuntimeError("no home")),
            self.assertRaisesRegex(login_command.AuthError, "set XMC_CREDENTIALS_FILE"),
        ):
            login_command.credentials_path({})

    def test_run_login_browser_flow_exchanges_the_verifier_for_the_loopback_challenge(
        self,
    ) -> None:
        opener = urllib.request.build_opener(login_command._NoRedirect)
        challenges: list[str] = []

        def open_url(login_url: str) -> bool:
            qs = urllib.parse.parse_qs(urllib.parse.urlsplit(login_url).query)
            challenges.append(qs["code_challenge"][0])
            port, state = int(qs["port"][0]), qs["state"][0]

            def callback() -> None:
                with contextlib.suppress(urllib.error.HTTPError):  # The 302 redirect.
                    opener.open(f"http://127.0.0.1:{port}/callback?state={state}&code=c-1")

            threading.Thread(target=callback, daemon=True).start()
            return True

        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        env, out = self._env(), io.StringIO()
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant) as exchange,
        ):
            rc = login_command.run_login(
                ["--timeout", "5"], env=env, out=out, err=io.StringIO(), open_url=open_url
            )

        self.assertEqual(rc, 0)
        server, code, verifier = exchange.call_args.args
        digest = hashlib.sha256(verifier.encode("ascii")).digest()
        self.assertEqual((server, code), (_SERVER, "c-1"))
        self.assertEqual(
            challenges, [base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")]
        )
        self.assertIn(f"Logged in to {_SERVER} as u@example.com", out.getvalue())
        cached = login_command.load_credentials(login_command.credentials_path(env))
        self.assertEqual(cached[_SERVER]["access_token"], "jwt")

    def test_load_credentials_rejects_unreadable_corrupt_or_unknown_files(self) -> None:
        path = pathlib.Path(self._xdg) / "credentials.json"
        cases = [
            ("{not json", "Corrupt credentials file"),
            ('{"version": 2, "deployments": {}}', "Unsupported credentials schema"),
            ("[]", "Unsupported credentials schema"),
        ]
        for content, message in cases:
            with self.subTest(content=content):
                path.write_text(content, encoding="utf-8")
                os.chmod(path, 0o600)
                with self.assertRaisesRegex(login_command.AuthError, message):
                    login_command.load_credentials(path)
        path.write_text('{"version": 1, "deployments": []}', encoding="utf-8")
        self.assertEqual(login_command.load_credentials(path), {})
        if os.name == "posix":
            link = pathlib.Path(self._xdg) / "link.json"
            link.symlink_to(path)
            with self.assertRaisesRegex(login_command.AuthError, "Refusing to read"):
                login_command.load_credentials(link)

    def test_credentials_roundtrip_permissions_and_relative_xdg(self) -> None:
        self.assertEqual(
            login_command.credentials_path({"XDG_CONFIG_HOME": "relative"}),
            pathlib.Path.home() / ".config" / "xmc" / "credentials.json",
        )
        self.assertEqual(
            login_command.credentials_path({"XMC_CREDENTIALS_FILE": "relative"}),
            pathlib.Path.home() / ".config" / "xmc" / "credentials.json",
        )
        custom_file = os.path.join(self._xdg, "custom_creds.json")
        self.assertEqual(
            login_command.credentials_path({"XMC_CREDENTIALS_FILE": custom_file}),
            pathlib.Path(custom_file),
        )
        path = login_command.credentials_path({"XDG_CONFIG_HOME": self._xdg})
        self.assertEqual(path, pathlib.Path(self._xdg) / "xmc" / "credentials.json")
        self.assertEqual(login_command.load_credentials(path), {})
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        login_command.save_credentials(path, {"https://z.example.com": entry, _SERVER: entry})
        raw = path.read_text(encoding="utf-8")
        self.assertLess(raw.index(_SERVER), raw.index("https://z.example.com"))
        self.assertEqual(
            login_command.load_credentials(path),
            {"https://z.example.com": entry, _SERVER: entry},
        )
        if os.name == "posix":
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)
            os.chmod(path, 0o644)
            with self.assertRaisesRegex(login_command.AuthError, "xmc logout --all"):
                login_command.load_credentials(path)

    def test_save_credentials_reports_write_errors(self) -> None:
        env = self._env()
        path = login_command.credentials_path(env)
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        login_command.save_credentials(path, {_SERVER: entry})
        err = io.StringIO()
        with mock.patch.object(
            login_command.tempfile, "mkstemp", side_effect=PermissionError("denied")
        ):
            with self.assertRaisesRegex(login_command.AuthError, "Cannot write credentials file"):
                login_command.save_credentials(path, {})
            self.assertEqual(login_command.run_logout([], env=env, out=io.StringIO(), err=err), 1)
        self.assertIn("Error: Cannot write credentials file", err.getvalue())
        self.assertEqual(login_command.load_credentials(path), {_SERVER: entry})

    def test_save_credentials_keeps_the_mode_of_an_existing_directory(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        for mode in (0o755, 0o775, 0o1777):
            with self.subTest(mode=oct(mode)):
                existing = pathlib.Path(self.create_tempdir().full_path)
                os.chmod(existing, mode)
                path = existing / "credentials.json"

                login_command.save_credentials(path, {})

                self.assertEqual(stat.S_IMODE(existing.stat().st_mode), mode)
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
                self.assertEqual(login_command.load_credentials(path), {})

    def test_write_credentials_closes_fd_and_unlinks_tmp_on_fdopen_failure(self) -> None:
        path = pathlib.Path(self._xdg) / "xmc" / "credentials.json"
        with (
            mock.patch.object(os, "fdopen", side_effect=OSError("fdopen failed")),
            mock.patch.object(os, "close", wraps=os.close) as close_mock,
            self.assertRaisesRegex(login_command.AuthError, "Cannot write credentials file"),
        ):
            login_command.save_credentials(path, {})

        close_mock.assert_called_once()
        self.assertEqual(list(path.parent.glob(".credentials.*")), [])

    def test_run_login_reports_save_failure_after_token_exchange(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        with (
            mock.patch.object(login_command, "_probe_cli_login"),
            mock.patch.object(login_command, "exchange_code", return_value=grant),
            mock.patch.object(
                login_command.tempfile, "mkstemp", side_effect=PermissionError("denied")
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

    def test_run_login_rejects_non_positive_or_non_finite_timeout(self) -> None:
        for timeout in ("0", "-1", "nan", "inf"):
            with self.subTest(timeout=timeout):
                with (
                    contextlib.redirect_stderr(io.StringIO()) as stderr,
                    self.assertRaises(SystemExit) as exit_,
                ):
                    login_command.run_login(["--timeout", timeout], env=self._env())

                self.assertEqual(exit_.exception.code, 2)
                self.assertIn("--timeout must be a positive finite number", stderr.getvalue())

    def test_pkce_and_browser_availability(self) -> None:
        verifier, challenge = login_command.pkce_pair()
        self.assertGreaterEqual(len(verifier), 43)
        self.assertNotIn("=", challenge)
        self.assertTrue(login_command.browser_available({"DISPLAY": ":0"}))
        self.assertFalse(
            login_command.browser_available(
                {"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 55 1.2.3.5 22"}
            )
        )

    def test_await_loopback_code_handles_forged_and_genuine_callbacks(
        self,
    ) -> None:
        opener = urllib.request.build_opener(login_command._NoRedirect)

        def open_url(login_url: str) -> bool:
            qs = urllib.parse.parse_qs(urllib.parse.urlsplit(login_url).query)
            port, state = int(qs["port"][0]), qs["state"][0]

            def send_callbacks() -> None:
                with self.assertRaises(urllib.error.HTTPError) as bad:
                    opener.open(f"http://127.0.0.1:{port}/callback?state=wrong&code=bad")
                self.assertEqual(bad.exception.code, 400)
                with self.assertRaises(urllib.error.HTTPError) as non_ascii:
                    opener.open(f"http://127.0.0.1:{port}/callback?state=%C3%BC&code=bad")
                self.assertEqual(non_ascii.exception.code, 400)
                with self.assertRaises(urllib.error.HTTPError) as empty:
                    opener.open(f"http://127.0.0.1:{port}/callback?state={state}")
                self.assertEqual(empty.exception.code, 400)
                with self.assertRaises(urllib.error.HTTPError) as redirect:
                    opener.open(f"http://127.0.0.1:{port}/callback?state={state}&code=good-code")
                self.assertEqual(redirect.exception.code, 302)

            threading.Thread(target=send_callbacks, daemon=True).start()
            return True

        code = login_command.await_loopback_code(
            _SERVER, "challenge-xyz", open_url, io.StringIO(), timeout=5.0
        )
        self.assertEqual(code, "good-code")

        def open_url_error(login_url: str) -> bool:
            qs = urllib.parse.parse_qs(urllib.parse.urlsplit(login_url).query)
            port, state = int(qs["port"][0]), qs["state"][0]

            def send_err() -> None:
                with self.assertRaises(urllib.error.HTTPError) as redirect:
                    opener.open(f"http://127.0.0.1:{port}/callback?state={state}&error=denied")
                self.assertEqual(redirect.exception.code, 302)

            threading.Thread(target=send_err, daemon=True).start()
            return True

        with self.assertRaisesRegex(login_command.AuthError, "denied"):
            login_command.await_loopback_code(
                _SERVER, "challenge-xyz", open_url_error, io.StringIO(), timeout=5.0
            )

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
                login_command.AuthError, r"Token exchange failed: HTTP 400\."
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
                    self.assertRaisesRegex(login_command.AuthError, pattern),
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
            self.assertRaisesRegex(login_command.AuthError, r"Token exchange failed: HTTP 502\."),
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
                    self.assertRaisesRegex(login_command.AuthError, "Malformed token response"),
                ):
                    login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        with (
            mock.patch.object(urllib.request, "urlopen", side_effect=OSError("reset")),
            self.assertRaisesRegex(login_command.AuthError, "Token exchange failed: reset"),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")
        with (
            mock.patch.object(
                urllib.request, "urlopen", side_effect=http.client.BadStatusLine("garbage")
            ),
            self.assertRaisesRegex(login_command.AuthError, "Token exchange failed: garbage"),
        ):
            login_command.exchange_code(_SERVER, "code-1", "verifier-1")

    def test_probe_cli_login_reports_malformed_http_responses(self) -> None:
        with (
            mock.patch.object(
                urllib.request.OpenerDirector,
                "open",
                side_effect=http.client.BadStatusLine("garbage"),
            ),
            self.assertRaisesRegex(login_command.AuthError, "Cannot reach .*: garbage"),
        ):
            login_command._probe_cli_login(_SERVER)

    def test_run_logout_reports_each_outcome(self) -> None:
        env = self._env()
        path = login_command.credentials_path(env)
        cases = [
            (["--all"], "Logged out of all XMC deployments.\n"),
            ([], f"Not logged in to {_SERVER}.\n"),
        ]
        for argv, message in cases:
            with self.subTest(argv=argv):
                out = io.StringIO()
                self.assertEqual(
                    login_command.run_logout(argv, env=env, out=out, err=io.StringIO()), 0
                )
                self.assertEqual(out.getvalue(), message)
                self.assertFalse(path.exists())
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        other = "https://other.example.com"
        login_command.save_credentials(path, {_SERVER: entry, other: entry})
        out = io.StringIO()

        self.assertEqual(login_command.run_logout([], env=env, out=out, err=io.StringIO()), 0)

        self.assertEqual(out.getvalue(), f"Logged out of {_SERVER}.\n")
        self.assertEqual(login_command.load_credentials(path), {other: entry})

    def test_await_loopback_code_times_out_or_reports_unopenable_browser(self) -> None:
        with self.assertRaisesRegex(login_command.AuthError, "Timed out"):
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

    def test_run_logout_all_reports_an_unreadable_credentials_location(self) -> None:
        err = io.StringIO()
        with mock.patch.object(pathlib.Path, "exists", side_effect=PermissionError("denied")):
            rc = login_command.run_logout(["--all"], env=self._env(), out=io.StringIO(), err=err)

        self.assertEqual(rc, 1)
        self.assertIn("Error: Cannot access credentials file", err.getvalue())

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
                with self.assertRaisesRegex(login_command.AuthError, "requires XMC >= 0.16.0"):
                    login_command._probe_cli_login(_SERVER)
        with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=_http_err(302)):
            with self.assertRaisesRegex(login_command.AuthError, "HTTP 302"):
                login_command._probe_cli_login(_SERVER)
        with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=OSError("down")):
            with self.assertRaisesRegex(login_command.AuthError, "Cannot reach"):
                login_command._probe_cli_login(_SERVER)
        with self.assertRaisesRegex(login_command.AuthError, "Cannot reach"):
            login_command._probe_cli_login("https://\u00fc..example.com")  # Not IDNA-encodable.

    def test_run_login_manual_status_print_token_and_logout(self) -> None:
        out, err, env = io.StringIO(), io.StringIO(), self._env()
        grant = {"access_token": "jwt", "email": "u@example.com", "expires_in": 900}
        no_open = mock.Mock(side_effect=AssertionError("unexpected open_url"))
        with (
            mock.patch.object(login_command, "_probe_cli_login") as mock_probe,
            mock.patch.object(login_command, "exchange_code", return_value=grant),
        ):
            self.assertEqual(
                login_command.run_login(
                    ["--no-browser"],
                    env=env,
                    out=out,
                    err=err,
                    open_url=no_open,
                    prompt=lambda _: "c",
                ),
                0,
            )
            self.assertNotIn("No local browser detected", out.getvalue())
            ssh_out = io.StringIO()
            self.assertEqual(
                login_command.run_login(
                    [],
                    env=self._env(SSH_CONNECTION="1.2.3.4 55 1.2.3.5 22"),
                    out=ssh_out,
                    err=err,
                    open_url=no_open,
                    prompt=lambda _: "c",
                ),
                0,
            )
            self.assertIn("No local browser detected", ssh_out.getvalue())
            rc = login_command.run_login(
                [],
                env=env,
                out=out,
                err=err,
                open_url=lambda _: False,
                prompt=lambda _: "c",
            )
        self.assertEqual(mock_probe.call_count, 3)
        self.assertEqual(rc, 0)
        self.assertIn("falling back to manual login", out.getvalue())
        status_out, token_out = io.StringIO(), io.StringIO()
        self.assertEqual(
            login_command.run_login(["--status"], env=env, out=status_out, err=err),
            0,
        )
        self.assertIn("u@example.com", status_out.getvalue())
        self.assertNotIn("jwt", status_out.getvalue())
        self.assertEqual(
            login_command.run_login(["--print-token"], env=env, out=token_out, err=err),
            0,
        )
        self.assertEqual(token_out.getvalue(), "jwt\n")
        self.assertEqual(login_command.run_logout([], env=env, out=out, err=err), 0)
        self.assertEqual(login_command.run_login(["--status"], env=env, out=out, err=err), 1)

    def test_expired_or_malformed_token_and_logout_all(self) -> None:
        env = self._env()
        path = login_command.credentials_path(env)
        # Cached expiry is a wall-clock Unix timestamp, matching login_command.
        now = int(time.time())  # noqa: TID251
        for bad_entry in (
            {
                "access_token": "old",
                "email": "u@x.io",
                "expires_at": now - 10,
            },
            {"access_token": "old", "email": "u@x.io", "expires_at": "not-an-int"},
            {"access_token": "old", "expires_at": now + 900},
            "not-a-dict",
        ):
            login_command.save_credentials(path, {_SERVER: bad_entry})  # type: ignore[dict-item]
            out, err = io.StringIO(), io.StringIO()
            self.assertEqual(
                login_command.run_login(["--print-token"], env=env, out=out, err=err),
                1,
            )
            self.assertEqual(login_command.run_login(["--status"], env=env, out=out, err=err), 1)
        self.assertEqual(
            login_command.run_logout(["--all"], env=env, out=io.StringIO(), err=io.StringIO()),
            0,
        )
        self.assertEqual(login_command.load_credentials(path), {})

    def test_full_argv_entrypoints_exit_only_on_failure(self) -> None:
        # run_login and run_logout bind os.environ as their default, so patching it
        # in place reaches them.
        with mock.patch.dict(os.environ, self._env(), clear=True):
            with self.assertRaises(SystemExit) as login_exit:
                login_command.login_command(["xmanager", "login", "--status"])
            path = login_command.credentials_path(os.environ)
            entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
            login_command.save_credentials(path, {_SERVER: entry})
            # Success returns normally instead of raising SystemExit.
            login_command.logout_command(["xmanager", "logout", "--all"])
            self.assertEqual(login_command.load_credentials(path), {})
        with (
            mock.patch.dict(os.environ, {"XDG_CONFIG_HOME": self._xdg}, clear=True),
            self.assertRaises(SystemExit) as logout_exit,
        ):
            login_command.logout_command(["xmanager", "logout"])
        self.assertEqual(login_exit.exception.code, 1)
        self.assertEqual(logout_exit.exception.code, 1)


if __name__ == "__main__":
    absltest.main()
