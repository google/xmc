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
import os
import sys
import tempfile
import unittest
from collections.abc import Sequence
from typing import TextIO
from unittest import mock

from xm_cloud.cli import login_command, logout_command, main


class MainTest(unittest.TestCase):
    """Unit tests for the `xmc` dispatcher."""

    def _main(self, argv: list[str]) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        code = main.main(argv, out=out, err=err)
        return code, out.getvalue(), err.getvalue()

    def test_registers_login_and_logout(self) -> None:
        self.assertIs(main.SUBCOMMANDS["login"].run, login_command.run_login)
        self.assertIs(main.SUBCOMMANDS["logout"].run, logout_command.run_logout)

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

    def test_routes_subcommand_process_stream_writes_to_given_streams(self) -> None:
        def run(argv: Sequence[str], *, out: TextIO = sys.stdout, err: TextIO = sys.stderr) -> int:
            del argv, out, err  # Writes through the process streams instead.
            print("to stdout")
            print("to stderr", file=sys.stderr)
            return 0

        with mock.patch.object(main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}):
            code, out, err = self._main(["logout"])

        self.assertEqual((code, out, err), (0, "to stdout\n", "to stderr\n"))

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
        cases = [
            (SystemExit("boom"), 1, "boom\n"),
            (SystemExit(None), 0, ""),
            (SystemExit(2), 2, ""),
        ]
        for exc, expected_code, expected_err in cases:
            with self.subTest(code=exc.code):
                run = mock.Mock(side_effect=exc)
                with mock.patch.object(
                    main, "SUBCOMMANDS", {"logout": main.Subcommand("Logs out.", run)}
                ):
                    code, out, err = self._main(["logout"])

                self.assertEqual((code, out, err), (expected_code, "", expected_err))

    def test_unknown_command_is_escaped_on_a_strict_ascii_stderr(self) -> None:
        err = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")

        code = main.main(["\udcff\u00e9\x1b"], out=io.StringIO(), err=err)

        err.flush()
        self.assertEqual(code, 2)
        self.assertIn(b"unknown command '\\udcff\\xe9\\x1b'\n", err.buffer.getvalue())

    def test_subcommand_usage_errors_are_escaped_on_a_strict_ascii_stderr(self) -> None:
        err = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")

        code = main.main(["logout", "\udcff"], out=io.StringIO(), err=err)

        err.flush()
        self.assertEqual(code, 2)
        self.assertIn(b"unrecognized arguments: \\udcff\n", err.buffer.getvalue())

    def test_output_errors_exit_1_without_a_traceback(self) -> None:
        with tempfile.TemporaryDirectory() as xdg:
            env = {"XDG_CONFIG_HOME": xdg, "XMANAGER_UI_URL": "https://xmc.example.com"}
            cases = [
                (["--help"], _BrokenStream(write_error=BrokenPipeError(32, "Broken pipe"))),
                (["logout"], _BrokenStream(write_error=BrokenPipeError(32, "Broken pipe"))),
                (["logout", "--all"], _BrokenStream(flush_error=OSError(28, "No space left"))),
            ]
            for argv, out in cases:
                with self.subTest(argv=argv), mock.patch.dict(os.environ, env, clear=True):
                    err = io.StringIO()

                    code = main.main(argv, out=out, err=err)

                    self.assertEqual(code, 1)
                    self.assertRegex(err.getvalue(), r"^xmc: error: cannot write output: \[Errno")

    def test_output_errors_on_both_streams_exit_1(self) -> None:
        broken = _BrokenStream(write_error=BrokenPipeError(32, "Broken pipe"))

        self.assertEqual(main.main(["--help"], out=broken, err=broken), 1)
        self.assertEqual(main.main([], out=broken, err=broken), 1)

    def test_broken_pipe_on_a_real_fd_redirects_the_descriptor_to_devnull(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX broken pipe semantics only")
        r_fd, w_fd = os.pipe()
        os.close(r_fd)
        err = io.StringIO()
        with os.fdopen(w_fd, "w", encoding="utf-8", buffering=1) as out:
            code = main.main(["--help"], out=out, err=err)
            out.write("flushed on exit\n")
            out.flush()

        self.assertEqual(code, 1)
        self.assertIn("Broken pipe", err.getvalue())

    def test_err_write_failure_does_not_redirect_healthy_stdout(self) -> None:
        err = _BrokenStream(write_error=OSError(28, "No space left"))
        with tempfile.TemporaryFile("w+", encoding="utf-8") as out:
            code = main.main([], out=out, err=err)
            out.write("still readable\n")
            out.seek(0)
            contents = out.read()

        self.assertEqual(code, 1)
        self.assertIn("still readable\n", contents)

    def test_login_output_errors_exit_1_without_a_traceback(self) -> None:
        # Writing the sign-in URL fails, so the code prompt is never reached.
        env = {"XMANAGER_UI_URL": "https://xmc.example.com"}
        with (
            tempfile.TemporaryDirectory() as xdg,
            mock.patch.dict(os.environ, {"XDG_CONFIG_HOME": xdg, **env}, clear=True),
            mock.patch.object(login_command, "_probe_cli_login"),
        ):
            err = io.StringIO()
            out = _BrokenStream(write_error=BrokenPipeError(32, "Broken pipe"))

            code = main.main(["login", "--no-browser"], out=out, err=err)

        self.assertEqual(code, 1)
        self.assertEqual(
            err.getvalue(), "xmc: error: cannot write output: [Errno 32] Broken pipe\n"
        )


class _BrokenStream(io.StringIO):
    """A text stream whose `write` or `flush` fails like a closed pipe or full disk."""

    def __init__(
        self, *, write_error: OSError | None = None, flush_error: OSError | None = None
    ) -> None:
        super().__init__()
        self._write_error = write_error
        self._flush_error = flush_error

    def write(self, s: str) -> int:
        if self._write_error is not None:
            raise self._write_error
        return super().write(s)

    def flush(self) -> None:
        if self._flush_error is not None:
            raise self._flush_error
        super().flush()


if __name__ == "__main__":
    unittest.main()
