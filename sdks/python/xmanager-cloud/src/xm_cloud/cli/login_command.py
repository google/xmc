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
"""`xmc login` subcommand implementation (manual PKCE flow).

Uses only the standard library, so it works without `xmanager`.
"""

from __future__ import annotations

import argparse
import base64
import datetime
import getpass
import hashlib
import http.client
import json
import os
import pathlib
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping, Sequence
from typing import Any, TextIO

from xm_cloud.cli import credentials


def pkce_pair() -> tuple[str, str]:
    """Returns an RFC 7636 `(code_verifier, S256 code_challenge)` pair."""
    verifier = secrets.token_urlsafe(32)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return verifier, base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def build_login_url(server: str, state: str, challenge: str) -> str:
    """Builds the `/api/cli/login` URL for manual mode."""
    params = [
        ("mode", "manual"),
        ("state", state),
        ("code_challenge", challenge),
        ("code_challenge_method", "S256"),
    ]
    return f"{server}/api/cli/login?{urllib.parse.urlencode(params)}"


def _format_http_error(exc: urllib.error.HTTPError) -> str:
    """Extracts an OAuth error description from an HTTP error body if present."""
    try:
        raw = exc.read(4096).decode("utf-8", errors="replace")
        payload = json.loads(raw)
    except (OSError, ValueError):
        return f"HTTP {exc.code}"
    if isinstance(payload, dict):
        detail = payload.get("error_description") or payload.get("error")
        if isinstance(detail, str) and detail.strip():
            return f"{detail.strip()} (HTTP {exc.code})"
    return f"HTTP {exc.code}"


def exchange_code(server: str, code: str, verifier: str) -> dict[str, Any]:
    """Exchanges a one-time `code` and PKCE `verifier` at `/api/cli/token`."""
    body = urllib.parse.urlencode(
        {
            "grant_type": "authorization_code",
            "code": code,
            "code_verifier": verifier,
        }
    ).encode("ascii")
    headers = {
        "Content-Type": "application/x-www-form-urlencoded",
        "Accept": "application/json",
    }
    req = urllib.request.Request(
        f"{server}/api/cli/token", data=body, headers=headers, method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raise credentials.AuthError(f"Token exchange failed: {_format_http_error(exc)}.") from None
    except (OSError, ValueError, http.client.HTTPException) as exc:
        raise credentials.AuthError(f"Token exchange failed: {exc}") from None
    return _validated_grant(payload)


def _validated_grant(p: Any) -> dict[str, Any]:
    """Returns the access token, email and lifetime from a `/api/cli/token` response."""
    if not isinstance(p, dict):
        raise credentials.AuthError("Malformed token response from server.")
    tok, email, exp = p.get("access_token"), p.get("email"), p.get("expires_in")
    valid = (
        isinstance(tok, str)
        and bool(tok)
        and isinstance(email, str)
        and bool(email)
        and isinstance(exp, int)
        and not isinstance(exp, bool)
        and exp > 0
        and str(p.get("token_type", "")).lower() == "bearer"
    )
    if not valid:
        raise credentials.AuthError("Malformed token response from server.")
    return {"access_token": tok, "email": email, "expires_in": exp}


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Disables automatic HTTP redirect following during preflight probes."""

    def redirect_request(
        self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str
    ) -> None:
        """Returns `None` so `urllib` surfaces the 3xx response directly."""
        del req, fp, code, msg, headers, newurl
        return


def _probe_cli_login(server: str) -> None:
    """Verifies that `server` is reachable and supports `/api/cli/login`."""
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        with opener.open(f"{server}/api/cli/login", timeout=10):
            return
    except urllib.error.HTTPError as exc:
        if exc.code == 400:
            return
        if exc.code in (401, 404, 405, 501):
            raise credentials.AuthError(
                f"Server {server} does not support `xmc login` (HTTP {exc.code};"
                " requires XMC >= 0.16.0). Use XMC_CLIENT_SA + IAP_CLIENT_ID or"
                " XMC_AUTH_TOKEN, or upgrade the XMC backend."
            ) from None
        raise credentials.AuthError(
            f"Server {server} returned HTTP {exc.code} on `/api/cli/login`."
        ) from None
    except (OSError, ValueError, http.client.HTTPException) as exc:  # ValueError: bad IDNA host.
        raise credentials.AuthError(f"Cannot reach {server}: {exc}") from None


def _interactive_login(
    server: str,
    prompt: Callable[[str], str],
    out: TextIO,
) -> dict[str, Any]:
    """Executes manual PKCE login and returns the token grant."""
    _probe_cli_login(server)
    verifier, challenge = pkce_pair()
    url = build_login_url(server, secrets.token_urlsafe(24), challenge)
    out.write(
        f"Open this URL in any browser to sign in to {server}:\n  {url}\n"
        "Never share the one-time code with anyone.\n"
    )
    try:
        code = prompt("Enter authorization code: ").strip()
    except EOFError:  # Closed or non-interactive stdin.
        code = ""
    if not code:
        raise credentials.AuthError("No authorization code entered.")
    return exchange_code(server, code, verifier)


def _format_expiry(expires_at: int) -> str:
    """Formats `expires_at` in local time, or as a raw timestamp when out of range."""
    try:
        utc = datetime.datetime.fromtimestamp(expires_at, datetime.UTC)
    except (OverflowError, OSError, ValueError):
        return f"Unix time {expires_at}"
    return utc.astimezone().strftime("%Y-%m-%d %H:%M %Z")


def _parse_login_args(
    argv: Sequence[str],
    out: TextIO = sys.stdout,
    err: TextIO = sys.stderr,
) -> argparse.Namespace | int:
    """Parses and validates CLI arguments for `xmc login`."""
    parser = argparse.ArgumentParser(
        prog="xmc login",
        description=(
            "Authenticate with an XMC deployment (requires XMC >= 0.16.0) and"
            " cache credentials in $XMC_CREDENTIALS_FILE or"
            " $XDG_CONFIG_HOME/xmc/credentials.json"
            " (default ~/.config/xmc/credentials.json)."
        ),
    )
    return credentials.parse_cli_args(parser, argv, out, err)


def _cache_grant(
    path: pathlib.Path,
    deployments: dict[str, dict[str, Any]],
    server: str,
    grant: Mapping[str, Any],
) -> int:
    """Stores `grant` for `server` in the credentials cache and returns its expiry."""
    # The cache stores a wall-clock Unix timestamp, so time.monotonic() would not fit.
    expires_at = int(time.time()) + int(grant["expires_in"])  # noqa: TID251
    deployments[server] = {
        "access_token": grant["access_token"],
        "email": grant["email"],
        "expires_at": expires_at,
    }
    credentials.save_credentials(path, deployments)
    return expires_at


def run_login(
    argv: Sequence[str],
    env: Mapping[str, str] = os.environ,
    out: TextIO = sys.stdout,
    err: TextIO = sys.stderr,
    prompt: Callable[[str], str] = getpass.getpass,
) -> int:
    """Parses `xmc login` flags, executes manual login, and returns exit code."""
    parsed = _parse_login_args(argv, out, err)
    if isinstance(parsed, int):
        return parsed
    try:
        server, path = credentials.resolve_server(env), credentials.credentials_path(env)
        deployments = credentials.load_credentials(path)
        grant = _interactive_login(server, prompt, out)
        expires_at = _cache_grant(path, deployments, server, grant)
        out.write(
            f"Logged in to {server} as {grant['email']} until {_format_expiry(expires_at)}.\n"
        )
        return 0
    except credentials.AuthError as exc:
        err.write(f"Error: {exc}\n")
        return 1


def login_command(argv: Sequence[str]) -> None:
    """Runs `login` from a full `<program> login ...` argv and exits on failure.

    This keeps the call shape that the core `xmanager login` command uses, so it
    can forward to this module.

    Args:
        argv: The full command line, including the program and command names.
    """
    if code := run_login(argv[2:]):
        sys.exit(code)
