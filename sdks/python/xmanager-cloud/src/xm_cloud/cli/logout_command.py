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
import os
import sys
from collections.abc import Mapping, Sequence
from typing import TextIO

from xm_cloud.cli import credentials


def run_logout(
    argv: Sequence[str],
    env: Mapping[str, str] = os.environ,
    out: TextIO = sys.stdout,
    err: TextIO = sys.stderr,
) -> int:
    """Parses `xmc logout` flags, clears credentials, and returns exit code."""
    parser = argparse.ArgumentParser(
        prog="xmc logout",
        description=(
            "Clear cached XMC credentials from $XMC_CREDENTIALS_FILE or"
            " $XDG_CONFIG_HOME/xmc/credentials.json"
            " (default ~/.config/xmc/credentials.json)."
        ),
    )
    parser.add_argument("--all", action="store_true")
    parsed = credentials.parse_cli_args(parser, argv, out, err)
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


def logout_command(argv: Sequence[str]) -> None:
    """Runs `logout` from a full `<program> logout ...` argv and exits on failure.

    Args:
        argv: The full command line, including the program and command names.
    """
    if code := run_logout(argv[2:]):
        sys.exit(code)
