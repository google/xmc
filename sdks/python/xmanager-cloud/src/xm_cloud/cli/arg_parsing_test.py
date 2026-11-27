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
"""Unit tests for `xm_cloud.cli.arg_parsing`."""

from __future__ import annotations

import argparse
import io
import unittest
from unittest import mock

from xm_cloud.cli import arg_parsing


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="xmc test")
    parser.add_argument("--all", action="store_true")
    return parser


class ArgParsingTest(unittest.TestCase):
    """Unit tests for `xm_cloud.cli.arg_parsing`."""

    def test_exit_code_maps_system_exit_codes(self) -> None:
        cases = [(None, 0, ""), (0, 0, ""), (2, 2, ""), ("boom", 1, "boom\n")]
        for code, expected, message in cases:
            with self.subTest(code=code):
                err = io.StringIO()

                self.assertEqual(arg_parsing.exit_code(SystemExit(code), err), expected)

                self.assertEqual(err.getvalue(), message)

    def test_parse_args_returns_the_namespace(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        parsed = arg_parsing.parse_args(_parser(), ["--all"], out, err)

        self.assertEqual(parsed, argparse.Namespace(all=True))
        self.assertEqual((out.getvalue(), err.getvalue()), ("", ""))

    def test_parse_args_writes_help_to_out_and_returns_zero(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        self.assertEqual(arg_parsing.parse_args(_parser(), ["--help"], out, err), 0)

        self.assertTrue(out.getvalue().startswith("usage: xmc test "))
        self.assertEqual(err.getvalue(), "")

    def test_parse_args_writes_usage_errors_to_err_and_returns_two(self) -> None:
        out, err = io.StringIO(), io.StringIO()

        self.assertEqual(arg_parsing.parse_args(_parser(), ["--bogus"], out, err), 2)

        self.assertEqual(out.getvalue(), "")
        self.assertIn("xmc test: error: unrecognized arguments: --bogus", err.getvalue())

    def test_parse_args_maps_a_system_exit_from_the_parser(self) -> None:
        cases = [
            (SystemExit(None), 0, ""),
            (SystemExit(2), 2, ""),
            (SystemExit("boom"), 1, "boom\n"),
        ]
        for exc, expected, message in cases:
            parser = _parser()
            err = io.StringIO()
            with (
                self.subTest(code=exc.code),
                mock.patch.object(parser, "parse_args", side_effect=exc),
            ):
                self.assertEqual(arg_parsing.parse_args(parser, [], io.StringIO(), err), expected)

                self.assertEqual(err.getvalue(), message)

    def test_parse_args_escapes_echoed_arguments_on_a_strict_ascii_stream(self) -> None:
        out = io.StringIO()
        err = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")

        code = arg_parsing.parse_args(_parser(), ["\udcff", "\x1b[2J", "\u00e9"], out, err)

        err.flush()
        self.assertEqual((code, out.getvalue()), (2, ""))
        written = err.buffer.getvalue()
        self.assertIn(b"unrecognized arguments: \\udcff \\x1b[2J \\xe9\n", written)
        self.assertNotIn(b"\x1b", written)

    def test_parse_args_writes_help_through_terminal_text(self) -> None:
        parser = argparse.ArgumentParser(prog="xmc test", description="caf\u00e9 \x1b[2J")
        out = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")
        err = io.StringIO()

        self.assertEqual(arg_parsing.parse_args(parser, ["--help"], out, err), 0)

        out.flush()
        self.assertEqual(err.getvalue(), "")
        self.assertIn(b"caf\\xe9 \\x1b[2J", out.buffer.getvalue())

    def test_exit_code_escapes_a_string_code(self) -> None:
        err = io.TextIOWrapper(io.BytesIO(), encoding="ascii", errors="strict")

        self.assertEqual(arg_parsing.exit_code(SystemExit("bad \x1b[2J \udcff"), err), 1)

        err.flush()
        self.assertEqual(err.buffer.getvalue(), b"bad \\x1b[2J \\udcff\n")


if __name__ == "__main__":
    unittest.main()
