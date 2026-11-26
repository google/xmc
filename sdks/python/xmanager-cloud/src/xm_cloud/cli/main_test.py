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

import io
import unittest
from unittest import mock

from xm_cloud.cli import login_command, main


class MainTest(unittest.TestCase):
    def _main(self, argv: list[str]) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        code = main.main(argv, out=out, err=err)
        return code, out.getvalue(), err.getvalue()

    def test_registers_logout(self) -> None:
        self.assertEqual(main.SUBCOMMANDS["logout"].run, login_command.run_logout)

    def test_help_lists_every_subcommand_on_stdout(self) -> None:
        for flag in ("-h", "--help"):
            with self.subTest(flag=flag):
                code, out, err = self._main([flag])

                self.assertEqual((code, err), (0, ""))
                self.assertTrue(out.startswith("usage: xmc {logout} ...\n"))
                for name, command in main.SUBCOMMANDS.items():
                    self.assertIn(f"  {name:<8}{command.summary}\n", out)

    def test_missing_command_prints_usage_to_stderr(self) -> None:
        code, out, err = self._main([])

        self.assertEqual((code, out, err), (2, "", main.format_usage()))

    def test_unknown_command_is_a_usage_error(self) -> None:
        code, out, err = self._main(["stop", "--help"])

        self.assertEqual((code, out), (2, ""))
        self.assertEqual(err, f"xmc: error: unknown command 'stop'\n{main.format_usage()}")

    def test_dispatches_remaining_arguments_and_streams_to_subcommand(self) -> None:
        run = mock.Mock(return_value=3)
        out_stream, err_stream = io.StringIO(), io.StringIO()
        subcommands = {"logout": main.Subcommand("Logs out.", run)}
        with mock.patch.object(main, "SUBCOMMANDS", subcommands):
            code = main.main(
                ["logout", "--all"],
                out=out_stream,
                err=err_stream,
            )

        self.assertEqual((code, out_stream.getvalue(), err_stream.getvalue()), (3, "", ""))
        run.assert_called_once_with(
            ["--all"],
            out=out_stream,
            err=err_stream,
        )

    def test_defaults_to_process_arguments(self) -> None:
        run = mock.Mock(return_value=0)
        with (
            mock.patch.object(main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}),
            mock.patch.object(main.sys, "argv", ["xmc", "logout", "--all"]),
        ):
            self.assertEqual(main.main(), 0)

        run.assert_called_once_with(
            ["--all"],
            out=main.sys.stdout,
            err=main.sys.stderr,
        )

    def test_ctrl_c_exits_130_without_a_traceback(self) -> None:
        run = mock.Mock(side_effect=KeyboardInterrupt)
        with mock.patch.object(main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}):
            code, out, err = self._main(["logout"])

        self.assertEqual((code, out, err), (130, "", "\nxmc: interrupted\n"))

    def test_subcommand_help_comes_from_the_subcommand_parser(self) -> None:
        code, out, err = self._main(["logout", "--help"])

        self.assertEqual((code, err), (0, ""))
        self.assertTrue(out.startswith("usage: xmc logout "))

    def test_subcommand_system_exit_from_subcommand_is_returned(self) -> None:
        for exc, expected in ((SystemExit("boom"), 1), (SystemExit(None), 0)):
            with self.subTest(code=exc.code):
                run = mock.Mock(side_effect=exc)
                with mock.patch.object(
                    main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}
                ):
                    code, out, err = self._main(["logout"])

                self.assertEqual((code, out, err), (expected, "", ""))


if __name__ == "__main__":
    unittest.main()
