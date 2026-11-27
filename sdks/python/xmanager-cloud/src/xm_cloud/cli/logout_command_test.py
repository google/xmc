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
"""Unit tests for `xm_cloud.cli.logout_command`."""

from __future__ import annotations

import io
import json
import os
import pathlib
import stat
import sys
import tempfile
import unittest
from unittest import mock

from xm_cloud.cli import credentials, logout_command

_SERVER = "https://xmc.example.com"
_ENTRY = {"access_token": "t", "email": "u@example.com", "expires_at": 999}


class LogoutCommandTest(unittest.TestCase):
    """Unit tests for `xmc logout`."""

    def setUp(self) -> None:
        super().setUp()
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self._xdg = temp_dir.name

    def _env(self, **extra: str) -> dict[str, str]:
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_UI_URL": _SERVER}
        env.update(extra)
        return env

    def _run(self, argv: list[str], env: dict[str, str]) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        code = logout_command.run_logout(argv, env=env, out=out, err=err)
        return code, out.getvalue(), err.getvalue()

    def test_run_logout_help_returns_zero_without_exiting(self) -> None:
        code, out, err = self._run(["--help"], self._env())

        self.assertEqual((code, err), (0, ""))
        self.assertTrue(out.startswith("usage: xmc logout "))

    def test_run_logout_rejects_unknown_flag(self) -> None:
        code, out, err = self._run(["--bogus"], self._env())

        self.assertEqual((code, out), (2, ""))
        self.assertIn("unrecognized arguments: --bogus", err)

    def test_run_logout_reports_corrupt_credentials_file(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.write_text("{not json", encoding="utf-8")
        if os.name == "posix":
            os.chmod(path, 0o600)

        code, out, err = self._run([], env)

        self.assertEqual((code, out), (1, ""))
        self.assertIn("Error: Corrupt credentials file", err)

    def test_run_logout_reports_write_errors(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        credentials.save_credentials(path, {_SERVER: _ENTRY})
        with mock.patch.object(
            credentials.tempfile, "mkstemp", side_effect=PermissionError("denied")
        ):
            code, _, err = self._run([], env)

        self.assertEqual(code, 1)
        self.assertIn("Error: Cannot write credentials file", err)
        self.assertEqual(credentials.load_credentials(path), {_SERVER: _ENTRY})

    def test_run_logout_without_a_server_reports_no_server(self) -> None:
        code, out, err = self._run([], {"XDG_CONFIG_HOME": self._xdg})

        self.assertEqual((code, out), (1, ""))
        self.assertEqual(err, f"Error: {credentials.NO_SERVER_MESSAGE}\n")

    def test_run_logout_when_not_logged_in_leaves_no_file(self) -> None:
        env = self._env()

        code, out, err = self._run([], env)

        self.assertEqual((code, out, err), (0, f"Not logged in to {_SERVER}.\n", ""))
        self.assertFalse(credentials.credentials_path(env).exists())

    def test_run_logout_removes_only_the_current_server(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        other = "https://other.example.com"
        credentials.save_credentials(path, {_SERVER: _ENTRY, other: _ENTRY})

        code, out, err = self._run([], env)

        self.assertEqual((code, out, err), (0, f"Logged out of {_SERVER}.\n", ""))
        self.assertEqual(credentials.load_credentials(path), {other: _ENTRY})

    def test_run_logout_defaults_to_process_env_and_streams(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        with (
            mock.patch.dict(os.environ, self._env(), clear=True),
            mock.patch.object(sys, "stdout", out),
            mock.patch.object(sys, "stderr", err),
        ):
            self.assertEqual(logout_command.run_logout([]), 0)

        self.assertEqual((out.getvalue(), err.getvalue()), (f"Not logged in to {_SERVER}.\n", ""))

    def test_run_logout_all_without_a_file_does_not_create_one(self) -> None:
        env = self._env()

        code, out, err = self._run(["--all"], env)

        self.assertEqual((code, out, err), (0, "Logged out of all XMC deployments.\n", ""))
        self.assertFalse(credentials.credentials_path(env).exists())

    def test_run_logout_all_clears_an_existing_file(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        credentials.save_credentials(path, {_SERVER: _ENTRY, "https://other.example.com": _ENTRY})

        code, out, err = self._run(["--all"], env)

        self.assertEqual((code, out, err), (0, "Logged out of all XMC deployments.\n", ""))
        self.assertEqual(credentials.load_credentials(path), {})
        if os.name == "posix":
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)

    def test_run_logout_all_replaces_dangling_symlink(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX symlinks only")
        env = self._env()
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.symlink_to(path.parent / "missing-target.json")

        code, out, err = self._run(["--all"], env)

        self.assertEqual((code, out, err), (0, "Logged out of all XMC deployments.\n", ""))
        self.assertFalse(path.is_symlink())
        self.assertTrue(stat.S_ISREG(path.stat().st_mode))
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(credentials.load_credentials(path), {})

    def test_run_logout_all_reports_an_unreadable_credentials_location(self) -> None:
        with mock.patch.object(pathlib.Path, "exists", side_effect=PermissionError("denied")):
            code, _, err = self._run(["--all"], self._env())

        self.assertEqual(code, 1)
        self.assertIn("Error: Cannot access credentials file", err)

    def test_run_logout_all_recovers_from_bad_config_and_a_corrupt_open_file(self) -> None:
        # `--all` is the documented reset: it must work when the deployment cannot be
        # resolved and the cache is corrupt and group-readable.
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_ENDPOINT": "grpc-bad.example.com:443"}
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.write_text("{not json", encoding="utf-8")
        os.chmod(path, 0o644)

        code, out, err = self._run(["--all"], env)

        self.assertEqual((code, out, err), (0, "Logged out of all XMC deployments.\n", ""))
        self.assertEqual(credentials.load_credentials(path), {})

    def test_run_logout_escapes_untrusted_path_characters_on_a_strict_stream(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX file names only")
        # \udcff is how os.environ decodes a non-UTF-8 byte; ESC starts a terminal sequence.
        path = pathlib.Path(self._xdg) / "\udcff\x1b[2J.json"
        path.write_text("{}", encoding="utf-8")
        os.chmod(path, 0o644)
        out = io.TextIOWrapper(io.BytesIO(), encoding="utf-8", errors="strict")
        err = io.TextIOWrapper(io.BytesIO(), encoding="utf-8", errors="strict")

        code = logout_command.run_logout(
            [], env=self._env(XMC_CREDENTIALS_FILE=str(path)), out=out, err=err
        )

        err.flush()
        self.assertEqual(code, 1)
        self.assertIn(b"\\udcff\\x1b[2J.json (must not be accessible", err.buffer.getvalue())

    def test_run_logout_escapes_an_idn_origin_on_a_strict_ascii_stdout(self) -> None:
        idn_server = "https://b\u00fccher.example"
        env = self._env(XMANAGER_UI_URL=idn_server)
        path = credentials.credentials_path(env)
        for cached, message in ((False, b"Not logged in to"), (True, b"Logged out of")):
            with self.subTest(cached=cached):
                if cached:
                    credentials.save_credentials(path, {idn_server: _ENTRY})
                out = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")
                err = io.StringIO()

                code = logout_command.run_logout([], env=env, out=out, err=err)

                out.flush()
                self.assertEqual((code, err.getvalue()), (0, ""))
                expected = message + b" https://b\\xfccher.example.\n"
                self.assertEqual(out.buffer.getvalue(), expected)

    def test_run_logout_escapes_unsafe_arguments_on_a_strict_ascii_stderr(self) -> None:
        # argparse echoes argv; a surrogate (undecodable byte) or ESC must not escape raw.
        out = io.StringIO()
        err = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")

        code = logout_command.run_logout(
            ["\udcff", "\x1b]0;x\x07", "\u00e9"], env=self._env(), out=out, err=err
        )

        err.flush()
        self.assertEqual((code, out.getvalue()), (2, ""))
        written = err.buffer.getvalue()
        self.assertIn(b"unrecognized arguments: \\udcff \\x1b]0;x\\x07 \\xe9", written)
        self.assertNotIn(b"\x1b", written)

    def test_run_logout_reports_an_unserializable_sibling_entry(self) -> None:
        # Python 3.11/3.12 re-serialize with the recursive pure-Python encoder; the
        # nested sibling is loaded directly, since json.loads limits differ by version.
        nested: list[object] = []
        for _ in range(5000):
            nested = [nested]
        loaded = {_SERVER: dict(_ENTRY), "https://b.dev": {"x": nested}}
        with (
            mock.patch.object(credentials, "load_credentials", return_value=loaded),
            mock.patch.object(json.encoder, "c_make_encoder", None),
        ):
            code, out, err = self._run([], self._env())

        self.assertEqual((code, out), (1, ""))
        self.assertTrue(err.startswith("Error: Cannot write credentials file"), err)


if __name__ == "__main__":
    unittest.main()
