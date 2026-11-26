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
"""Argument parsing for `xmc` that returns exit codes instead of raising `SystemExit`."""

from __future__ import annotations

import argparse
import contextlib
from collections.abc import Sequence
from typing import TextIO


def exit_code(exc: SystemExit, err: TextIO) -> int:
    """Returns the process exit code for `exc`, matching how `sys.exit` treats its code.

    Args:
        exc: `SystemExit` raised by argparse or a subcommand.
        err: Stream that receives a non-integer code, as the interpreter prints it.

    Returns:
        0 for a `None` code, the code itself if it is an integer, or 1 otherwise.
    """
    if exc.code is None:
        return 0
    if isinstance(exc.code, int):
        return exc.code
    err.write(f"{exc.code}\n")
    return 1


def parse_args(
    parser: argparse.ArgumentParser,
    argv: Sequence[str],
    out: TextIO,
    err: TextIO,
) -> argparse.Namespace | int:
    """Parses `argv` with `parser`, redirecting argparse output to `out`/`err`.

    Args:
        parser: Configured argument parser.
        argv: Command-line arguments after the subcommand name.
        out: Stream for standard output (e.g., `--help`).
        err: Stream for usage errors.

    Returns:
        Parsed `argparse.Namespace`, or the `exit_code` if argparse exited.
    """
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            return parser.parse_args(list(argv))
    except SystemExit as exc:
        return exit_code(exc, err)
