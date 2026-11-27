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
"""Entry point of the `xmc` console script, which dispatches to subcommands.

Each subcommand parses its own flags, so `xmc <command> --help` shows the
command's options and `xmc --help` lists the commands.
"""

from __future__ import annotations

import contextlib
import os
import sys
from collections.abc import Mapping, Sequence
from typing import NamedTuple, Protocol, TextIO

from xm_cloud.cli import arg_parsing, logout_command, terminal_text


class SubcommandRunner(Protocol):
    """Callable signature implemented by every `xmc` subcommand."""

    def __call__(
        self,
        argv: Sequence[str],
        *,
        out: TextIO = ...,
        err: TextIO = ...,
    ) -> int: ...


class Subcommand(NamedTuple):
    """An `xmc` subcommand: a one-line summary and the function that runs it."""

    summary: str
    run: SubcommandRunner


SUBCOMMANDS: Mapping[str, Subcommand] = {
    "logout": Subcommand("Clears cached XMC credentials.", logout_command.run_logout),
}
_HELP_FLAGS = frozenset({"-h", "--help"})


def format_usage() -> str:
    """Returns the `xmc` usage text that lists every subcommand."""
    lines = [f"usage: xmc {{{','.join(SUBCOMMANDS)}}} ...", "", "commands:"]
    lines += [f"  {name:<8}{command.summary}" for name, command in SUBCOMMANDS.items()]
    lines += ["", "Run `xmc <command> --help` for the options of a command."]
    return "\n".join(lines) + "\n"


def main(
    argv: Sequence[str] | None = None,
    out: TextIO | None = None,
    err: TextIO | None = None,
) -> int:
    """Runs the `xmc` subcommand named by the first argument.

    Args:
        argv: Arguments after the program name. Defaults to `sys.argv[1:]`.
        out: Standard output stream. Defaults to `sys.stdout`.
        err: Standard error stream. Defaults to `sys.stderr`.

    Returns:
        The `_dispatch` exit code, 130 on Ctrl-C, or 1 (without a traceback) when
        writing or flushing `out`/`err` raises `OSError`, e.g. `BrokenPipeError`.
    """
    args = list(sys.argv[1:] if argv is None else argv)
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    try:
        code = _dispatch(args, out, err)
        out.flush()  # Surface a closed pipe here, not in the interpreter's exit flush.
        return code
    except KeyboardInterrupt:
        _write_quietly(err, "\nxmc: interrupted\n")
        return 130
    except OSError as exc:
        try:
            out.flush()
        except OSError:
            _discard_unwritten_output(out)
        except ValueError:
            pass
        _write_quietly(err, f"xmc: error: cannot write output: {exc}\n")
        return 1


def _dispatch(args: list[str], out: TextIO, err: TextIO) -> int:
    """Runs the subcommand named by `args[0]` with the process streams redirected.

    Args:
        args: Arguments after the program name.
        out: Standard output stream.
        err: Standard error stream.

    Returns:
        The subcommand's exit code (or the code of a `SystemExit` it raises, with a
        non-integer code written to `err` and mapped to 1), 0 for `--help`, or 2
        for a missing or unknown subcommand.

    Raises:
        OSError: If writing to `out` or `err` fails.
    """
    if not args:
        err.write(format_usage())
        return 2
    if args[0] in _HELP_FLAGS:
        out.write(format_usage())
        return 0
    if (command := SUBCOMMANDS.get(args[0])) is None:
        terminal_text.write(err, f"xmc: error: unknown command {args[0]!r}\n{format_usage()}")
        return 2
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            return command.run(args[1:], out=out, err=err)
    except SystemExit as exc:
        return arg_parsing.exit_code(exc, err)


def _write_quietly(stream: TextIO, text: str) -> None:
    """Writes `text` to `stream` through `terminal_text`, ignoring a failing stream.

    Args:
        stream: Output stream that may itself be broken.
        text: Message to write.
    """
    with contextlib.suppress(OSError, ValueError):
        terminal_text.write(stream, text)
        stream.flush()


def _discard_unwritten_output(out: TextIO) -> None:
    """Points the descriptor of `out` at os.devnull after an output failure.

    Otherwise the interpreter's exit-time flush of the buffered output fails again
    and prints `Exception ignored ... BrokenPipeError`. A stream without a file
    descriptor (such as `io.StringIO`) is left alone.

    Args:
        out: Standard output stream whose write or flush failed.
    """
    with contextlib.suppress(OSError, ValueError):
        fd = out.fileno()
        devnull = os.open(os.devnull, os.O_WRONLY)
        if devnull != fd:
            try:
                os.dup2(devnull, fd)
            finally:
                os.close(devnull)


if __name__ == "__main__":
    sys.exit(main())
