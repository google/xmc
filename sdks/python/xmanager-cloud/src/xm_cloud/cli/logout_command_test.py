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

import argparse
import io
import os
import pathlib
import stat
import tempfile
import unittest
from unittest import mock

from xm_cloud.cli import credentials, logout_command

_SERVER = "https://xmc.example.com"


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

    def test_parse_cli_args_maps_system_exit_none_to_zero(self) -> None:
        parser = argparse.ArgumentParser(prog="xmc logout")
        with mock.patch.object(parser, "parse_args", side_effect=SystemExit(None)):
            self.assertEqual(
                logout_command.parse_cli_args(parser, [], io.StringIO(), io.StringIO()), 0
            )

    def test_run_logout_help_returns_zero_without_exiting(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        code = logout_command.run_logout(["--help"], env=self._env(), out=out, err=err)
        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertTrue(out.getvalue().startswith("usage: xmc logout "))

    def test_run_logout_rejects_unknown_flag(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        code = logout_command.run_logout(["--bogus"], env=self._env(), out=out, err=err)
        self.assertEqual((code, out.getvalue()), (2, ""))
        self.assertIn("unrecognized arguments: --bogus", err.getvalue())

    def test_run_logout_reports_corrupt_credentials_file(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.write_text("{not json", encoding="utf-8")
        if os.name == "posix":
            os.chmod(path, 0o600)
        out, err = io.StringIO(), io.StringIO()

        code = logout_command.run_logout([], env=env, out=out, err=err)

        self.assertEqual((code, out.getvalue()), (1, ""))
        self.assertIn("Error: Corrupt credentials file", err.getvalue())

    def test_run_logout_reports_write_errors(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        credentials.save_credentials(path, {_SERVER: entry})
        err = io.StringIO()
        with mock.patch.object(
            credentials.tempfile, "mkstemp", side_effect=PermissionError("denied")
        ):
            with self.assertRaisesRegex(credentials.AuthError, "Cannot write credentials file"):
                credentials.save_credentials(path, {})
            self.assertEqual(logout_command.run_logout([], env=env, out=io.StringIO(), err=err), 1)
        self.assertIn("Error: Cannot write credentials file", err.getvalue())
        self.assertEqual(credentials.load_credentials(path), {_SERVER: entry})

    def test_run_logout_reports_each_outcome(self) -> None:
        env = self._env()
        path = credentials.credentials_path(env)
        cases = [
            (["--all"], "Logged out of all XMC deployments.\n"),
            ([], f"Not logged in to {_SERVER}.\n"),
        ]
        for argv, message in cases:
            with self.subTest(argv=argv):
                out = io.StringIO()
                self.assertEqual(
                    logout_command.run_logout(argv, env=env, out=out, err=io.StringIO()), 0
                )
                self.assertEqual(out.getvalue(), message)
                self.assertFalse(path.exists())
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        other = "https://other.example.com"
        credentials.save_credentials(path, {_SERVER: entry, other: entry})
        out = io.StringIO()

        self.assertEqual(logout_command.run_logout([], env=env, out=out, err=io.StringIO()), 0)

        self.assertEqual(out.getvalue(), f"Logged out of {_SERVER}.\n")
        self.assertEqual(credentials.load_credentials(path), {other: entry})

    def test_run_logout_all_replaces_dangling_symlink(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX symlinks only")
        env = self._env()
        path = credentials.credentials_path(env)
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        path.symlink_to(path.parent / "missing-target.json")
        out, err = io.StringIO(), io.StringIO()

        code = logout_command.run_logout(["--all"], env=env, out=out, err=err)

        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertEqual(out.getvalue(), "Logged out of all XMC deployments.\n")
        self.assertFalse(path.is_symlink())
        self.assertTrue(stat.S_ISREG(path.stat().st_mode))
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(credentials.load_credentials(path), {})

    def test_run_logout_all_reports_an_unreadable_credentials_location(self) -> None:
        err = io.StringIO()
        with mock.patch.object(pathlib.Path, "exists", side_effect=PermissionError("denied")):
            rc = logout_command.run_logout(["--all"], env=self._env(), out=io.StringIO(), err=err)

        self.assertEqual(rc, 1)
        self.assertIn("Error: Cannot access credentials file", err.getvalue())


if __name__ == "__main__":
    unittest.main()
