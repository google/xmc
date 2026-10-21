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
import dataclasses
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from xmc.auth import AuthError, cli
from xmc.auth.credentials_store import CredentialsStore, StoredCredential

_NOW = 1_800_000_000
_SERVER = "https://xmc.example.com"
_ENV = {"XMC_API_URL": _SERVER}
_CACHED = StoredCredential(_SERVER, "tok-cached", "cached@example.com", _NOW + 3600)
_EXPIRED = StoredCredential(_SERVER, "tok-old", "old@example.com", _NOW)


def _write_cache(path: Path, credential: StoredCredential) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    payload = {"version": 1, "deployments": {credential.server: dataclasses.asdict(credential)}}
    path.write_text(json.dumps(payload), encoding="utf-8")


class CliTest(unittest.TestCase):
    """Tests subcommand behavior and exit codes with injected collaborators."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.cache_path = Path(tmp.name) / "credentials.json"
        self.store = CredentialsStore(self.cache_path, io.StringIO())
        self.out, self.err = io.StringIO(), io.StringIO()
        self.env = dict(_ENV)

    def _run(self, *argv: str) -> int:
        ctx = cli.CliContext(self.env, self.store, lambda: float(_NOW), self.out, self.err)
        return cli.run_cli(list(argv), ctx)

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

    def test_interrupted_exits_130(self) -> None:
        """Ctrl-C exits 130."""
        with mock.patch.object(self.store, "get", side_effect=KeyboardInterrupt):
            self.assertEqual(self._run("print-token"), 130)

    def test_auth_error_exits_1(self) -> None:
        """AuthError prints a message and exits 1."""
        with mock.patch.object(self.store, "get", side_effect=AuthError("broken")):
            self.assertEqual(self._run("print-token"), 1)
        self.assertEqual(self.err.getvalue(), "Error: broken\n")

    def test_print_token(self) -> None:
        """print-token prints only a valid token to stdout."""
        _write_cache(self.cache_path, _CACHED)
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
                    _write_cache(self.cache_path, cached)
                self.assertEqual(self._run("print-token", "--server", server), 1)
                self.assertEqual(self.out.getvalue(), "")
                self.assertIn("xmc login", self.err.getvalue())


class MainTest(unittest.TestCase):
    """Tests the console-script entry point wiring."""

    def test_main_uses_xdg_cache(self) -> None:
        """main() reads the cache under XDG_CONFIG_HOME and resolves XMC_API_URL."""
        with tempfile.TemporaryDirectory() as tmp:
            _write_cache(
                Path(tmp) / "xmc" / "credentials.json",
                StoredCredential(_SERVER, "tok-main", "u@example.com", 4_000_000_000),
            )
            env = {"XDG_CONFIG_HOME": tmp, "XMC_API_URL": _SERVER}
            out = io.StringIO()
            with mock.patch.dict(os.environ, env, clear=True), contextlib.redirect_stdout(out):
                self.assertEqual(cli.main(["print-token"]), 0)
        self.assertEqual(out.getvalue(), "tok-main\n")


if __name__ == "__main__":
    unittest.main()
