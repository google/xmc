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

"""`xmc` command line: login and print-token."""

from __future__ import annotations

import argparse
import dataclasses
import datetime
import getpass
import os
import sys
import webbrowser
from collections.abc import Callable, Mapping, Sequence
from typing import TextIO

from xmc.auth import token_client
from xmc.auth.browser_detection import browser_likely_available
from xmc.auth.credentials_store import (
    CredentialsStore,
    StoredCredential,
    default_credentials_path,
)
from xmc.auth.deployment_server import resolve_server
from xmc.auth.errors import AuthError
from xmc.auth.login_flow import DEFAULT_TIMEOUT_SECONDS, LoginDependencies, run_login


@dataclasses.dataclass(frozen=True)
class CliContext:
    """Process-level collaborators of the CLI, injectable for tests."""

    env: Mapping[str, str]
    store: CredentialsStore
    login_deps: LoginDependencies
    clock: Callable[[], float]  # Wall-clock Unix seconds.
    out: TextIO
    err: TextIO


def _printable(text: str) -> str:
    """Drops non-printable characters (e.g. terminal escapes) from server-provided text."""
    return "".join(char for char in text if char.isprintable())


def _format_expiry(expires_at: int) -> str:
    """Formats a Unix timestamp in local time."""
    # An aware UTC datetime converts unambiguously, even across DST transitions.
    expiry = datetime.datetime.fromtimestamp(expires_at, datetime.timezone.utc)
    return expiry.astimezone().strftime("%Y-%m-%d %H:%M %Z")


def _positive_seconds(value: str) -> float:
    """Parses a strictly positive number of seconds for argparse."""
    if (seconds := float(value)) <= 0:
        raise argparse.ArgumentTypeError("must be a positive number of seconds")
    return seconds


def _build_parser() -> argparse.ArgumentParser:
    """Builds the `xmc` argument parser."""
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--server",
        help="XMC deployment URL (default: $XMC_API_URL, $XMANAGER_UI_URL, $XMANAGER_ENDPOINT)",
    )
    parser = argparse.ArgumentParser(prog="xmc", description="Authenticate to XMC as yourself.")
    sub = parser.add_subparsers(dest="command", required=True)
    login = sub.add_parser("login", parents=[common], help="Sign in with your browser.")
    login.add_argument(
        "--no-browser", action="store_true", help="Sign in on any device and paste back a code."
    )
    login.add_argument(
        "--timeout",
        type=_positive_seconds,
        default=DEFAULT_TIMEOUT_SECONDS,
        help=f"Seconds to wait for the browser (default: {DEFAULT_TIMEOUT_SECONDS}).",
    )
    sub.add_parser("print-token", parents=[common], help="Print the token for XMC_AUTH_TOKEN.")
    return parser


def _login(args: argparse.Namespace, server: str, ctx: CliContext) -> int:
    """Runs `xmc login` and caches the issued token."""
    cached = ctx.store.get(server)
    use_browser = not args.no_browser and browser_likely_available(ctx.env)
    if not (args.no_browser or use_browser):
        ctx.out.write(
            "No local browser detected (SSH session or no display); using manual login.\n"
        )
    hint = cached.email if cached else None
    grant = run_login(server, ctx.login_deps, use_browser, args.timeout, hint)
    expires_at = int(ctx.clock()) + grant.expires_in
    ctx.store.put(StoredCredential(server, grant.access_token, grant.email, expires_at))
    ctx.out.write(
        f"Logged in to {server} as {_printable(grant.email)} until {_format_expiry(expires_at)}.\n"
        "To use it with XManager: export XMC_AUTH_TOKEN=$(xmc print-token)\n"
    )
    return 0


def _print_token(_args: argparse.Namespace, server: str, ctx: CliContext) -> int:
    """Runs `xmc print-token`, the only command that ever prints a token."""
    cached = ctx.store.get(server)
    if cached is None or cached.is_expired(ctx.clock()):
        ctx.err.write(f"No valid login for {server}. Run `xmc login`.\n")
        return 1
    ctx.out.write(f"{cached.access_token}\n")
    return 0


_COMMANDS: dict[str, Callable[[argparse.Namespace, str, CliContext], int]] = {
    "login": _login,
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
    login_deps = LoginDependencies(
        sys.stdout, webbrowser.open, getpass.getpass, token_client.exchange_code
    )
    ctx = CliContext(
        env=os.environ,
        store=CredentialsStore(default_credentials_path(os.environ)),
        login_deps=login_deps,
        clock=lambda: datetime.datetime.now(datetime.timezone.utc).timestamp(),
        out=sys.stdout,
        err=sys.stderr,
    )
    return run_cli(argv, ctx)
