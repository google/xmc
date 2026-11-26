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
import os
import pathlib
from unittest import mock

from absl.testing import absltest

from xm_cloud.cli import credentials, logout_command

_SERVER = "https://xmc.example.com"


# absl-py ships no type information to mypy here, so absltest.TestCase is typed as Any.
class LogoutCommandTest(absltest.TestCase):  # type: ignore[misc]
    """Unit tests for `xmc logout`."""

    def setUp(self) -> None:
        super().setUp()
        self._xdg = self.create_tempdir().full_path

    def _env(self, **extra: str) -> dict[str, str]:
        env = {"XDG_CONFIG_HOME": self._xdg, "XMANAGER_UI_URL": _SERVER}
        env.update(extra)
        return env

    def test_run_logout_help_returns_zero_without_exiting(self) -> None:
        out, err = io.StringIO(), io.StringIO()
        code = logout_command.run_logout(["--help"], env=self._env(), out=out, err=err)
        self.assertEqual((code, err.getvalue()), (0, ""))
        self.assertTrue(out.getvalue().startswith("usage: xmc logout "))

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

    def test_run_logout_all_reports_an_unreadable_credentials_location(self) -> None:
        err = io.StringIO()
        with mock.patch.object(pathlib.Path, "exists", side_effect=PermissionError("denied")):
            rc = logout_command.run_logout(["--all"], env=self._env(), out=io.StringIO(), err=err)

        self.assertEqual(rc, 1)
        self.assertIn("Error: Cannot access credentials file", err.getvalue())

    def test_logout_command_exits_only_on_failure(self) -> None:
        # run_logout binds os.environ as its default, so patching it in place reaches it.
        with mock.patch.dict(os.environ, self._env(), clear=True):
            path = credentials.credentials_path(os.environ)
            entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
            credentials.save_credentials(path, {_SERVER: entry})
            # Success returns normally instead of raising SystemExit.
            logout_command.logout_command(["xmanager", "logout", "--all"])
            self.assertEqual(credentials.load_credentials(path), {})
        with (
            mock.patch.dict(os.environ, {"XDG_CONFIG_HOME": self._xdg}, clear=True),
            self.assertRaises(SystemExit) as logout_exit,
        ):
            logout_command.logout_command(["xmanager", "logout"])
        self.assertEqual(logout_exit.exception.code, 1)


if __name__ == "__main__":
    absltest.main()
