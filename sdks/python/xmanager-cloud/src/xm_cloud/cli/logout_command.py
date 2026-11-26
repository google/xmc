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
import pathlib
import sys
from collections.abc import Mapping, Sequence
from typing import TextIO

from xm_cloud.cli import arg_parsing, credentials


def _build_parser() -> argparse.ArgumentParser:
    """Returns the `xmc logout` argument parser."""
    parser = argparse.ArgumentParser(
        prog="xmc logout",
        description=(
            "Clear cached XMC credentials from $XMC_CREDENTIALS_FILE or"
            " $XDG_CONFIG_HOME/xmc/credentials.json"
            " (default ~/.config/xmc/credentials.json)."
        ),
    )
    parser.add_argument("--all", action="store_true")
    return parser


def _logout(path: pathlib.Path, env: Mapping[str, str], *, all_deployments: bool) -> str:
    """Removes cached credentials from `path` and returns the message to print.

    Raises:
        credentials.AuthError: If the deployment or the credentials file is invalid.
    """
    if all_deployments:
        if credentials.credentials_file_exists(path):
            credentials.save_credentials(path, {})
        return "Logged out of all XMC deployments.\n"
    server = credentials.resolve_server(env)
    deployments = credentials.load_credentials(path)
    if server not in deployments:
        return f"Not logged in to {server}.\n"
    del deployments[server]
    credentials.save_credentials(path, deployments)
    return f"Logged out of {server}.\n"


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
    parsed = arg_parsing.parse_args(_build_parser(), argv, out, err)
    if isinstance(parsed, int):
        return parsed
    try:
        path = credentials.credentials_path(env)
        out.write(_logout(path, env, all_deployments=parsed.all))
    except credentials.AuthError as exc:
        err.write(f"Error: {exc}\n")
        return 1
    return 0
