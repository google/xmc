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

"""Unit tests for xmc.auth.cli."""

from __future__ import annotations

import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from xmc.auth import cli
from xmc.auth.credentials_store import CredentialsStore, StoredCredential
from xmc.auth.errors import AuthError
from xmc.auth.login_flow import LoginDependencies
from xmc.auth.token_client import TokenGrant

_NOW = 1_800_000_000
_SERVER = "https://xmc.example.com"
_ENV = {"XMC_API_URL": _SERVER, "DISPLAY": ":0"}
_GRANT = TokenGrant(access_token="tok-new", email="user@example.com", expires_in=43200)
_CACHED = StoredCredential(_SERVER, "tok-cached", "cached@example.com", _NOW + 3600)
_EXPIRED = StoredCredential(_SERVER, "tok-old", "old@example.com", _NOW)


class CliTest(unittest.TestCase):
    """Tests subcommand behavior and exit codes with injected collaborators."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.store = CredentialsStore(Path(tmp.name) / "credentials.json", io.StringIO())
        self.out, self.err = io.StringIO(), io.StringIO()
        self.env = dict(_ENV)
        run_login = mock.patch.object(cli, "run_login", return_value=_GRANT)
        self.run_login = run_login.start()
        self.addCleanup(run_login.stop)

    def _run(self, *argv: str) -> int:
        deps = LoginDependencies(self.out, mock.Mock(), mock.Mock(), mock.Mock())
        ctx = cli.CliContext(self.env, self.store, deps, lambda: float(_NOW), self.out, self.err)
        return cli.run_cli(list(argv), ctx)

    def test_login_caches_token_without_printing_it(self) -> None:
        """login stores the grant, prints the account, and never prints the token."""
        self.assertEqual(self._run("login"), 0)
        self.assertEqual(
            self.store.get(_SERVER),
            StoredCredential(_SERVER, "tok-new", "user@example.com", _NOW + 43200),
        )
        self.assertIn("user@example.com", self.out.getvalue())
        self.assertIn("export XMC_AUTH_TOKEN=$(xmc print-token)", self.out.getvalue())
        self.assertNotIn("tok-new", self.out.getvalue() + self.err.getvalue())
        self.run_login.assert_called_once_with(_SERVER, mock.ANY, True, 300, None)

    def test_login_strips_control_characters_from_email(self) -> None:
        """Terminal escape sequences in a server-provided email are not printed."""
        self.run_login.return_value = TokenGrant("tok", "evil\x1b[2J@example.com\n", 60)
        self._run("login")
        self.assertIn("evil[2J@example.com until", self.out.getvalue())
        self.assertNotIn("\x1b", self.out.getvalue())

    def test_login_passes_cached_email_as_hint_and_flags(self) -> None:
        """Re-login uses the cached email as login_hint and honors flags."""
        self.store.put(_CACHED)
        self._run(
            "login", "--no-browser", "--timeout", "30", "--server", "https://XMC.example.com/"
        )
        self.run_login.assert_called_once_with(_SERVER, mock.ANY, False, 30.0, "cached@example.com")
        self.assertNotIn("No local browser", self.out.getvalue())

    def test_login_falls_back_without_browser(self) -> None:
        """Without a display, login announces and uses manual mode."""
        self.env = {"XMC_API_URL": _SERVER, "SSH_TTY": "/dev/pts/0"}
        self._run("login")
        self.assertIn("No local browser detected", self.out.getvalue())
        self.assertFalse(self.run_login.call_args.args[2])

    def test_login_failure_exits_1(self) -> None:
        """Login errors print a message and exit 1 without caching anything."""
        self.run_login.side_effect = AuthError("not authorized")
        self.assertEqual(self._run("login"), 1)
        self.assertEqual(self.err.getvalue(), "Error: not authorized\n")
        self.assertIsNone(self.store.get(_SERVER))

    def test_login_interrupted_exits_130(self) -> None:
        """Ctrl-C during login exits 130."""
        self.run_login.side_effect = KeyboardInterrupt
        self.assertEqual(self._run("login"), 130)

    def test_rejects_non_positive_timeout(self) -> None:
        """--timeout must be positive."""
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as ctx:
            self._run("login", "--timeout", "0")
        self.assertEqual(ctx.exception.code, 2)

    def test_requires_subcommand(self) -> None:
        """Running without a subcommand is a usage error."""
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as ctx:
            self._run()
        self.assertEqual(ctx.exception.code, 2)

    def test_missing_server_exits_1(self) -> None:
        """Commands fail cleanly when no server is configured."""
        self.env = {}
        self.assertEqual(self._run("print-token"), 1)
        self.assertIn("--server", self.err.getvalue())

    def test_print_token(self) -> None:
        """print-token prints only a valid token to stdout."""
        self.store.put(_CACHED)
        self.assertEqual(self._run("print-token"), 0)
        self.assertEqual(self.out.getvalue(), "tok-cached\n")

    def test_print_token_missing_expired_or_other_origin(self) -> None:
        """print-token exits 1 when missing, expired, or cached for a different origin."""
        cases = [
            (None, _SERVER),
            (_EXPIRED, _SERVER),
            (_CACHED, "https://xmc.example.com:8443"),
        ]
        for cached, server in cases:
            with self.subTest(cached=cached, server=server):
                if cached:
                    self.store.put(cached)
                self.assertEqual(self._run("print-token", "--server", server), 1)
                self.assertEqual(self.out.getvalue(), "")
                self.assertIn("xmc login", self.err.getvalue())


class MainTest(unittest.TestCase):
    """Tests the console-script entry point wiring."""

    def test_main_uses_xdg_cache(self) -> None:
        """main() reads the cache under XDG_CONFIG_HOME and resolves XMC_API_URL."""
        with tempfile.TemporaryDirectory() as tmp:
            store = CredentialsStore(Path(tmp) / "xmc" / "credentials.json")
            store.put(StoredCredential(_SERVER, "tok-main", "u@example.com", 4_000_000_000))
            env = {"XDG_CONFIG_HOME": tmp, "XMC_API_URL": _SERVER}
            out = io.StringIO()
            with mock.patch.dict(os.environ, env, clear=True), contextlib.redirect_stdout(out):
                self.assertEqual(cli.main(["print-token"]), 0)
        self.assertEqual(out.getvalue(), "tok-main\n")


if __name__ == "__main__":
    unittest.main()
