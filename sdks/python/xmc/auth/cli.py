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

"""`xmc` command line: print-token."""

from __future__ import annotations

import argparse
import dataclasses
import datetime
import os
import sys
from collections.abc import Callable, Mapping, Sequence
from typing import TextIO

from xmc.auth import AuthError
from xmc.auth.credentials_store import CredentialsStore, default_credentials_path
from xmc.auth.deployment_server import resolve_server


@dataclasses.dataclass(frozen=True)
class CliContext:
    """Process-level collaborators of the CLI, injectable for tests."""

    env: Mapping[str, str]
    store: CredentialsStore
    clock: Callable[[], float]  # Wall-clock Unix seconds.
    out: TextIO
    err: TextIO


def _build_parser() -> argparse.ArgumentParser:
    """Builds the `xmc` argument parser."""
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--server",
        help="XMC deployment URL (default: $XMC_API_URL, $XMANAGER_UI_URL, $XMC_DOMAIN)",
    )
    parser = argparse.ArgumentParser(prog="xmc", description="Authenticate to XMC as yourself.")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("print-token", parents=[common], help="Print the token for XMC_AUTH_TOKEN.")
    return parser


def _print_token(_args: argparse.Namespace, server: str, ctx: CliContext) -> int:
    """Runs `xmc print-token`, the only command that ever prints a token."""
    cached = ctx.store.get(server)
    if cached is None or cached.is_expired(ctx.clock()):
        ctx.err.write(f"No valid login for {server}. Run `xmc login`.\n")
        return 1
    ctx.out.write(f"{cached.access_token}\n")
    return 0


_COMMANDS: dict[str, Callable[[argparse.Namespace, str, CliContext], int]] = {
    "print-token": _print_token,
}


def run_cli(argv: Sequence[str] | None, ctx: CliContext) -> int:
    """Parses `argv` (None reads `sys.argv`), runs one subcommand, and returns its exit code."""
    args = _build_parser().parse_args(argv)
    try:
        server = resolve_server(args.server, ctx.env)
        return _COMMANDS[args.command](args, server, ctx)
    except AuthError as e:
        ctx.err.write(f"Error: {e}\n")
        return 1
    except KeyboardInterrupt:
        ctx.err.write("Aborted.\n")
        return 130


def main(argv: Sequence[str] | None = None) -> int:
    """Console-script entry point for `xmc`, wired to the real environment."""
    ctx = CliContext(
        env=os.environ,
        store=CredentialsStore(default_credentials_path(os.environ)),
        clock=lambda: datetime.datetime.now(datetime.timezone.utc).timestamp(),
        out=sys.stdout,
        err=sys.stderr,
    )
    return run_cli(argv, ctx)


if __name__ == "__main__":
    sys.exit(main())
