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
"""`xmc logout` subcommand implementation."""

from __future__ import annotations

import argparse
import contextlib
import os
import sys
from collections.abc import Mapping, Sequence
from typing import TextIO

from xm_cloud.cli import credentials


def parse_cli_args(
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
        err: Stream for error output.

    Returns:
        Parsed `argparse.Namespace`, or an integer exit code if parsing exited.
    """
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            return parser.parse_args(list(argv))
    except SystemExit as exc:
        return 0 if exc.code is None else (exc.code if isinstance(exc.code, int) else 1)


def run_logout(
    argv: Sequence[str],
    env: Mapping[str, str] | None = None,
    out: TextIO | None = None,
    err: TextIO | None = None,
) -> int:
    """Parses `xmc logout` flags, clears credentials, and returns an exit code.

    Args:
        argv: Arguments after `logout`.
        env: Environment mapping. Defaults to `os.environ`.
        out: Standard output stream. Defaults to `sys.stdout`.
        err: Standard error stream. Defaults to `sys.stderr`.

    Returns:
        0 on success or `--help`, 2 on argument usage errors, or 1 on `AuthError`.
    """
    env = os.environ if env is None else env
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    parser = argparse.ArgumentParser(
        prog="xmc logout",
        description=(
            "Clear cached XMC credentials from $XMC_CREDENTIALS_FILE or"
            " $XDG_CONFIG_HOME/xmc/credentials.json"
            " (default ~/.config/xmc/credentials.json)."
        ),
    )
    parser.add_argument("--all", action="store_true")
    parsed = parse_cli_args(parser, argv, out, err)
    if isinstance(parsed, int):
        return parsed
    args = parsed
    try:
        path = credentials.credentials_path(env)
        if args.all:
            if credentials.credentials_file_exists(path):
                credentials.save_credentials(path, {})
            out.write("Logged out of all XMC deployments.\n")
            return 0
        server = credentials.resolve_server(env)
        deployments = credentials.load_credentials(path)
        if server in deployments:
            del deployments[server]
            credentials.save_credentials(path, deployments)
            out.write(f"Logged out of {server}.\n")
        else:
            out.write(f"Not logged in to {server}.\n")
        return 0
    except credentials.AuthError as exc:
        err.write(f"Error: {exc}\n")
        return 1
