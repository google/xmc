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
import io
from collections.abc import Sequence
from typing import TextIO

from xm_cloud.cli import terminal_text


def exit_code(exc: SystemExit, err: TextIO) -> int:
    """Returns the process exit code for `exc`, matching how `sys.exit` treats its code.

    Args:
        exc: `SystemExit` raised by argparse or a subcommand.
        err: Stream that receives a non-integer code, as the interpreter prints it,
            through `terminal_text.write`.

    Returns:
        0 for a `None` code, the code itself if it is an integer, or 1 otherwise.
    """
    if exc.code is None:
        return 0
    if isinstance(exc.code, int):
        return exc.code
    terminal_text.write(err, f"{exc.code}\n")
    return 1


def parse_args(
    parser: argparse.ArgumentParser,
    argv: Sequence[str],
    out: TextIO,
    err: TextIO,
) -> argparse.Namespace | int:
    """Parses `argv`, writing argparse output to `out`/`err` via `terminal_text`.

    Args:
        parser: Configured argument parser.
        argv: Command-line arguments after the subcommand name.
        out: Stream for standard output (e.g., `--help`).
        err: Stream for usage errors.

    Returns:
        Parsed `argparse.Namespace`, or the `exit_code` if argparse exited.
    """
    captured_out, captured_err = io.StringIO(), io.StringIO()
    result: argparse.Namespace | SystemExit
    try:  # argparse output echoes `argv`; capture it so it is written safely.
        with contextlib.redirect_stdout(captured_out), contextlib.redirect_stderr(captured_err):
            result = parser.parse_args(list(argv))
    except SystemExit as exc:
        result = exc
    for stream, captured in ((out, captured_out), (err, captured_err)):
        if text := captured.getvalue():
            terminal_text.write(stream, text)
    return exit_code(result, err) if isinstance(result, SystemExit) else result
