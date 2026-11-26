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

import sys
from collections.abc import Mapping, Sequence
from typing import NamedTuple, Protocol, TextIO

from xm_cloud.cli import login_command


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
    "logout": Subcommand("Clears cached XMC credentials.", login_command.run_logout),
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
        out: Stream for the requested help text. Defaults to `sys.stdout`.
        err: Stream for usage errors. Defaults to `sys.stderr`.

    Returns:
        The subcommand's exit code, 0 for `--help`, 2 for a missing or unknown
        subcommand, or 130 when interrupted with Ctrl-C.
    """
    args = list(sys.argv[1:] if argv is None else argv)
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    if not args:
        err.write(format_usage())
        return 2
    if args[0] in _HELP_FLAGS:
        out.write(format_usage())
        return 0
    if (command := SUBCOMMANDS.get(args[0])) is None:
        err.write(f"xmc: error: unknown command {args[0]!r}\n{format_usage()}")
        return 2
    try:
        return command.run(args[1:], out=out, err=err)
    except KeyboardInterrupt:
        err.write("\nxmc: interrupted\n")
        return 130


if __name__ == "__main__":
    sys.exit(main())
