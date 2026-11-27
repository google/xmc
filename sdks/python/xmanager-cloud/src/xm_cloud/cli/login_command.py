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
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping, Sequence
from typing import Any, TextIO

from xm_cloud.cli import arg_parsing, credentials

# A token response is a few hundred bytes; the cap bounds memory and JSON parsing.
_MAX_TOKEN_RESPONSE_BYTES = 64 * 1024
# Only the start of an error body is needed to find its OAuth error description.
_MAX_ERROR_BODY_BYTES = 4096
# Exceptions a malformed or truncated HTTP response can raise while it is read or
# parsed. UnicodeDecodeError and json.JSONDecodeError are ValueErrors; RecursionError
# comes from deeply nested JSON.
_RESPONSE_ERRORS = (OSError, ValueError, RecursionError, http.client.HTTPException)


def pkce_pair() -> tuple[str, str]:
    """Generates an RFC 7636 PKCE pair.

    Returns:
        A `(code_verifier, code_challenge)` tuple, where the challenge is the
        unpadded base64url SHA-256 of the verifier (method `S256`).
    """
    verifier = secrets.token_urlsafe(32)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return verifier, base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def build_login_url(server: str, state: str, challenge: str) -> str:
    """Builds the `/api/cli/login` URL for the manual PKCE flow.

    Args:
        server: Deployment origin, e.g. `https://xmc.example.com`.
        state: Opaque CSRF state echoed back by the server.
        challenge: S256 PKCE code challenge from `pkce_pair`.

    Returns:
        The absolute sign-in URL to open in a browser.
    """
    params = [
        ("mode", "manual"),
        ("state", state),
        ("code_challenge", challenge),
        ("code_challenge_method", "S256"),
    ]
    return f"{server}/api/cli/login?{urllib.parse.urlencode(params)}"


def _format_http_error(exc: urllib.error.HTTPError) -> str:
    """Extracts an OAuth error description from an HTTP error body if present.

    Args:
        exc: The HTTP error whose body (at most `_MAX_ERROR_BODY_BYTES`) is read.

    Returns:
        `"<description> (HTTP <code>)"`, or `"HTTP <code>"` if the body is
        unreadable, truncated, not JSON, or has no error description.
    """
    try:
        raw = exc.read(_MAX_ERROR_BODY_BYTES).decode("utf-8", errors="replace")
        payload = json.loads(raw)
    except _RESPONSE_ERRORS:
        return f"HTTP {exc.code}"
    if isinstance(payload, dict):
        detail = payload.get("error_description") or payload.get("error")
        if isinstance(detail, str) and detail.strip():
            return f"{detail.strip()} (HTTP {exc.code})"
    return f"HTTP {exc.code}"


def _post_token_request(server: str, body: bytes) -> bytes:
    """POSTs `body` to `/api/cli/token` without following redirects.

    Args:
        server: Deployment origin.
        body: Form-encoded token request.

    Returns:
        The response body, at most `_MAX_TOKEN_RESPONSE_BYTES` + 1 bytes long.

    Raises:
        credentials.AuthError: On transport errors, an invalid URL, or a non-2xx
            or redirect response.
    """
    headers = {"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        req = urllib.request.Request(
            f"{server}/api/cli/token", data=body, headers=headers, method="POST"
        )
        with opener.open(req, timeout=30) as resp:
            return resp.read(_MAX_TOKEN_RESPONSE_BYTES + 1)
    except urllib.error.HTTPError as exc:
        raise credentials.AuthError(f"Token exchange failed: {_format_http_error(exc)}.") from None
    except _RESPONSE_ERRORS as exc:
        raise credentials.AuthError(f"Token exchange failed: {exc}") from None


def exchange_code(server: str, code: str, verifier: str) -> dict[str, Any]:
    """Exchanges a one-time authorization code at `/api/cli/token`.

    Args:
        server: Deployment origin.
        code: One-time authorization code shown in the browser.
        verifier: PKCE code verifier matching the challenge sent at login.

    Returns:
        Dict with `access_token`, `email`, and `expires_in` (seconds).

    Raises:
        credentials.AuthError: On transport errors, non-2xx or redirect responses,
            or oversized or malformed token responses.
    """
    form = {"grant_type": "authorization_code", "code": code, "code_verifier": verifier}
    raw = _post_token_request(server, urllib.parse.urlencode(form).encode("ascii"))
    if len(raw) > _MAX_TOKEN_RESPONSE_BYTES:
        raise credentials.AuthError("Token exchange failed: response too large.")
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (ValueError, RecursionError) as exc:
        raise credentials.AuthError(f"Token exchange failed: {exc}") from None
    return _validated_grant(payload)


def _validated_grant(p: Any) -> dict[str, Any]:
    """Returns the access token, email and lifetime from a `/api/cli/token` response.

    Args:
        p: Decoded JSON token response.

    Returns:
        Dict with the stripped `access_token` and `email`, and `expires_in`.

    Raises:
        credentials.AuthError: If `p` is not a Bearer grant with a non-blank token
            and email and a positive integer lifetime.
    """
    malformed = credentials.AuthError("Malformed token response from server.")
    if not isinstance(p, dict):
        raise malformed
    tok, email, exp = p.get("access_token"), p.get("email"), p.get("expires_in")
    if not isinstance(tok, str) or not isinstance(email, str):
        raise malformed
    tok, email = tok.strip(), email.strip()
    valid = (
        bool(tok)
        and bool(email)
        and isinstance(exp, int)
        and not isinstance(exp, bool)
        and exp > 0
        and str(p.get("token_type", "")).lower() == "bearer"
    )
    if not valid:
        raise malformed
    return {"access_token": tok, "email": email, "expires_in": exp}


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Disables HTTP redirect following so 3xx responses surface as `HTTPError`.

    Used for the preflight probe and the token exchange, where following a
    307/308 would replay the authorization code and PKCE verifier elsewhere.
    """

    def redirect_request(
        self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str
    ) -> None:
        """Returns `None` so `urllib` surfaces the 3xx response directly."""
        del req, fp, code, msg, headers, newurl
        return


def _probe_cli_login(server: str) -> None:
    """Verifies that `server` is reachable and supports `/api/cli/login`.

    XMC >= 0.16.0 answers a bare GET with HTTP 400 (missing parameters); it never
    returns 2xx, so a 2xx means the request reached something other than XMC
    (for example a single-page app fallback).

    Args:
        server: Deployment origin.

    Raises:
        credentials.AuthError: If `server` is unreachable, answers 2xx or 3xx, or
            does not support CLI login.
    """
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        with opener.open(f"{server}/api/cli/login", timeout=10) as resp:
            status = resp.status
    except urllib.error.HTTPError as exc:
        _check_probe_status(server, exc.code)
        return
    except _RESPONSE_ERRORS as exc:  # ValueError includes a bad IDNA host.
        raise credentials.AuthError(f"Cannot reach {server}: {exc}") from None
    raise credentials.AuthError(
        f"Server {server} returned unexpected HTTP {status} on `/api/cli/login`"
        " (expected HTTP 400 from XMC >= 0.16.0); check the configured deployment URL."
    )


def _check_probe_status(server: str, code: int) -> None:
    """Accepts the probe's HTTP 400 and rejects every other error status.

    Args:
        server: Deployment origin, used in error messages.
        code: HTTP status of the probe's error response.

    Raises:
        credentials.AuthError: Unless `code` is 400.
    """
    if code == 400:
        return
    if code in (401, 404, 405, 501):
        raise credentials.AuthError(
            f"Server {server} does not support `xmc login` (HTTP {code};"
            " requires XMC >= 0.16.0). Use XMC_CLIENT_SA + IAP_CLIENT_ID or"
            " XMC_AUTH_TOKEN, or upgrade the XMC backend."
        )
    raise credentials.AuthError(f"Server {server} returned HTTP {code} on `/api/cli/login`.")


def _interactive_login(
    server: str,
    prompt: Callable[[str], str],
    out: TextIO,
) -> dict[str, Any]:
    """Executes manual PKCE login and returns the token grant.

    Args:
        server: Deployment origin.
        prompt: Reads the authorization code.
        out: Stream for the sign-in instructions.

    Returns:
        The validated token grant from `exchange_code`.

    Raises:
        credentials.AuthError: If the probe or token exchange fails, or no code is
            entered.
    """
    _probe_cli_login(server)
    verifier, challenge = pkce_pair()
    url = build_login_url(server, secrets.token_urlsafe(24), challenge)
    out.write(
        f"Open this URL in any browser to sign in to {server}:\n  {url}\n"
        "Never share the one-time code with anyone.\n"
    )
    # The prompt may write to /dev/tty, so a buffered `out` must show the URL first.
    out.flush()
    try:
        code = prompt("Enter authorization code: ").strip()
    except EOFError:  # Closed or non-interactive stdin.
        code = ""
    if not code:
        raise credentials.AuthError("No authorization code entered.")
    return exchange_code(server, code, verifier)


def _format_expiry(expires_at: int) -> str:
    """Formats `expires_at` in local time, or as a raw timestamp when out of range.

    Args:
        expires_at: Unix timestamp in seconds.

    Returns:
        `"YYYY-MM-DD HH:MM <TZ>"` in local time, or `"Unix time <expires_at>"` if
        the timestamp cannot be represented in UTC or local time.
    """
    try:
        local = datetime.datetime.fromtimestamp(expires_at, datetime.UTC).astimezone()
    except (OverflowError, OSError, ValueError):
        return f"Unix time {expires_at}"
    return local.strftime("%Y-%m-%d %H:%M %Z")


def _parse_login_args(
    argv: Sequence[str],
    out: TextIO = sys.stdout,
    err: TextIO = sys.stderr,
) -> argparse.Namespace | int:
    """Parses and validates CLI arguments for `xmc login`.

    Args:
        argv: Subcommand arguments after `login`.
        out: Stream for `--help` output.
        err: Stream for usage errors.

    Returns:
        The parsed namespace, or an exit code if parsing ended early (`--help`
        or an invalid argument).
    """
    parser = argparse.ArgumentParser(
        prog="xmc login",
        description=(
            "Authenticate with an XMC deployment (requires XMC >= 0.16.0) and"
            " cache credentials in $XMC_CREDENTIALS_FILE or"
            " $XDG_CONFIG_HOME/xmc/credentials.json"
            " (default ~/.config/xmc/credentials.json)."
        ),
    )
    return arg_parsing.parse_args(parser, argv, out, err)


def _cache_grant(path: pathlib.Path, server: str, grant: Mapping[str, Any]) -> int:
    """Stores `grant` for `server` in the credentials cache and returns its expiry.

    The cache is re-read right before writing, so entries that a concurrent
    `xmc login`/`logout` wrote during the interactive flow are kept. Without a
    file lock, a write between this read and the replace can still be lost.

    Args:
        path: Credentials file.
        server: Deployment origin used as the cache key.
        grant: Validated grant from `exchange_code`.

    Returns:
        The cached expiry as a Unix timestamp in seconds.

    Raises:
        credentials.AuthError: If the cache cannot be read or written.
    """
    deployments = credentials.load_credentials(path)
    # The cache stores a wall-clock Unix timestamp, so a monotonic clock would not fit.
    now = int(datetime.datetime.now(datetime.UTC).timestamp())
    expires_at = now + int(grant["expires_in"])
    deployments[server] = {
        "access_token": grant["access_token"],
        "email": grant["email"],
        "expires_at": expires_at,
    }
    credentials.save_credentials(path, deployments)
    return expires_at


def run_login(
    argv: Sequence[str],
    env: Mapping[str, str] | None = None,
    out: TextIO | None = None,
    err: TextIO | None = None,
    prompt: Callable[[str], str] = getpass.getpass,
) -> int:
    """Runs `xmc login` (manual PKCE flow) and caches the resulting credentials.

    Args:
        argv: Subcommand arguments after `login`.
        env: Environment used to resolve the server and credentials path;
            defaults to `os.environ`.
        out: Stream for instructions and success output; defaults to `sys.stdout`.
        err: Stream for error messages; defaults to `sys.stderr`.
        prompt: Reads the authorization code; defaults to `getpass.getpass`.

    Returns:
        Process exit code: 0 on success or `--help`, 1 on authentication or
        cache errors, 2 on invalid arguments.
    """
    env = os.environ if env is None else env
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    parsed = _parse_login_args(argv, out, err)
    if isinstance(parsed, int):
        return parsed
    try:
        server, path = credentials.resolve_server(env), credentials.credentials_path(env)
        credentials.load_credentials(path)  # Fail on an unreadable cache before prompting.
        grant = _interactive_login(server, prompt, out)
        expires_at = _cache_grant(path, server, grant)
        out.write(
            f"Logged in to {server} as {grant['email']} until {_format_expiry(expires_at)}.\n"
        )
        return 0
    except credentials.AuthError as exc:
        err.write(f"Error: {exc}\n")
        return 1
