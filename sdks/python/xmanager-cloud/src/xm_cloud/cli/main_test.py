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

"""Unit tests for `xm_cloud.cli.main`."""

import contextlib
import io
import unittest
from unittest import mock

from xm_cloud.cli import login_command, main


class MainTest(unittest.TestCase):
    def _main(self, argv: list[str]) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        code = main.main(argv, out=out, err=err)
        return code, out.getvalue(), err.getvalue()

    def test_registers_login_and_logout(self) -> None:
        self.assertEqual(main.SUBCOMMANDS["login"].run, login_command.run_login)
        self.assertEqual(main.SUBCOMMANDS["logout"].run, login_command.run_logout)

    def test_help_lists_every_subcommand_on_stdout(self) -> None:
        for flag in ("-h", "--help"):
            with self.subTest(flag=flag):
                code, out, err = self._main([flag])

                self.assertEqual((code, err), (0, ""))
                self.assertTrue(out.startswith("usage: xmc {login,logout} ...\n"))
                for name, command in main.SUBCOMMANDS.items():
                    self.assertIn(f"  {name:<8}{command.summary}\n", out)

    def test_missing_command_prints_usage_to_stderr(self) -> None:
        code, out, err = self._main([])

        self.assertEqual((code, out, err), (2, "", main.format_usage()))

    def test_unknown_command_is_a_usage_error(self) -> None:
        code, out, err = self._main(["stop", "--help"])

        self.assertEqual((code, out), (2, ""))
        self.assertEqual(err, f"xmc: error: unknown command 'stop'\n{main.format_usage()}")

    def test_dispatches_remaining_arguments_and_returns_exit_code(self) -> None:
        run = mock.Mock(return_value=3)
        subcommands = {"login": main.Subcommand("Logs in.", run)}
        with mock.patch.object(main, "SUBCOMMANDS", subcommands):
            code, out, err = self._main(["login", "--status", "--no-browser"])

        self.assertEqual((code, out, err), (3, "", ""))
        run.assert_called_once_with(["--status", "--no-browser"])

    def test_defaults_to_process_arguments(self) -> None:
        run = mock.Mock(return_value=0)
        with (
            mock.patch.object(main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}),
            mock.patch.object(main.sys, "argv", ["xmc", "logout", "--all"]),
        ):
            self.assertEqual(main.main(), 0)

        run.assert_called_once_with(["--all"])

    def test_ctrl_c_exits_130_without_a_traceback(self) -> None:
        run = mock.Mock(side_effect=KeyboardInterrupt)
        with mock.patch.object(main, "SUBCOMMANDS", {"login": main.Subcommand("Logs in.", run)}):
            code, out, err = self._main(["login"])

        self.assertEqual((code, out, err), (130, "", "\nxmc: interrupted\n"))

    def test_subcommand_help_comes_from_the_subcommand_parser(self) -> None:
        for name in ("login", "logout"):
            with self.subTest(name=name):
                out = io.StringIO()
                with contextlib.redirect_stdout(out), self.assertRaises(SystemExit) as exit_:
                    main.main([name, "--help"])

                self.assertEqual(exit_.exception.code, 0)
                self.assertTrue(out.getvalue().startswith(f"usage: xmc {name} "))


if __name__ == "__main__":
    unittest.main()
