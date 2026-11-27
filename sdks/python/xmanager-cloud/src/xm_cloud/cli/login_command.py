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
"""`xmc login` subcommand implementation (browser loopback + manual PKCE flow).

Uses only the standard library, so it works without `xmanager`.
"""

from __future__ import annotations

import argparse
import base64
import datetime
import getpass
import hashlib
import http.client
import http.server
import json
import math
import os
import pathlib
import secrets
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser
from collections.abc import Callable, Mapping, Sequence
from typing import Any, TextIO, TypeVar

from xm_cloud.cli import arg_parsing, credentials, terminal_text

_T = TypeVar("_T")

if sys.platform == "win32":
    _TTY_ERRORS: tuple[type[Exception], ...] = ()
else:
    import termios

    _TTY_ERRORS = (termios.error,)

# A token response is a few hundred bytes; the cap bounds memory and JSON parsing.
_MAX_TOKEN_RESPONSE_BYTES = 64 * 1024
# Only the start of an error body is needed to find its OAuth error description.
_MAX_ERROR_BODY_BYTES = 4096
# Wall-clock limits for the whole probe and token request (DNS, connect, and every
# read); urllib's own `timeout` bounds only each socket operation.
_PROBE_TIMEOUT_SECONDS = 10.0
_TOKEN_TIMEOUT_SECONDS = 30.0
# XMC's longest CLI session (CLI_SESSION_TTL) is 720h. One year leaves headroom
# while keeping `expires_at` small enough for `xdash.auth` to convert to a float.
_MAX_EXPIRES_IN_SECONDS = 365 * 24 * 60 * 60
# `xdash.auth` (CLI_TOKEN_EXPIRY_BUFFER_SEC) treats a token as expired this many
# seconds early, so a shorter grant is unusable the moment it is cached.
_EXPIRY_SKEW_SECONDS = 30
# Exceptions a malformed or truncated HTTP response can raise while it is read or
# parsed. UnicodeDecodeError and json.JSONDecodeError are ValueErrors; RecursionError
# comes from deeply nested JSON.
_RESPONSE_ERRORS = (OSError, ValueError, RecursionError, http.client.HTTPException)
# Exceptions `getpass.getpass` can raise besides EOFError: OSError on a hung-up
# terminal, ValueError (including UnicodeDecodeError) on a closed or non-UTF-8
# stdin, AttributeError when `sys.stdin` is None, and termios.error.
_PROMPT_ERRORS = (OSError, ValueError, AttributeError, *_TTY_ERRORS)
_EXPIRY_SKEW_SECONDS = 60


def pkce_pair() -> tuple[str, str]:
    """Generates an RFC 7636 PKCE pair.

    Returns:
        A `(code_verifier, code_challenge)` tuple, where the challenge is the
        unpadded base64url SHA-256 of the verifier (method `S256`).
    """
    verifier = secrets.token_urlsafe(32)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return verifier, base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def browser_available(env: Mapping[str, str]) -> bool:
    """Reports whether a local GUI browser can likely be launched.

    Args:
        env: Environment to inspect for SSH sessions and display servers.

    Returns:
        False in an SSH session; otherwise True on macOS and Windows, or when
        `DISPLAY` or `WAYLAND_DISPLAY` is set.
    """
    if any(env.get(k) for k in ("SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY")):
        return False
    return sys.platform in ("darwin", "win32") or bool(
        env.get("DISPLAY") or env.get("WAYLAND_DISPLAY")
    )


def build_login_url(server: str, state: str, challenge: str, port: int | None = None) -> str:
    """Builds the `/api/cli/login` URL for loopback (`port`) or manual mode.

    Args:
        server: Deployment origin, e.g. `https://xmc.example.com`.
        state: Opaque CSRF state echoed back by the server.
        challenge: S256 PKCE code challenge from `pkce_pair`.
        port: Loopback callback port, or None for the manual (copy-paste) flow.

    Returns:
        The absolute sign-in URL to open in a browser.
    """
    params = [("port", str(port))] if port is not None else [("mode", "manual")]
    params += [
        ("state", state),
        ("code_challenge", challenge),
        ("code_challenge_method", "S256"),
    ]
    return f"{server}/api/cli/login?{urllib.parse.urlencode(params)}"


def _callback_query(target: str, state: str) -> dict[str, list[str]] | None:
    """Parses a loopback request target if it is `/callback` with the expected `state`.

    Args:
        target: The HTTP request target, e.g. `/callback?state=...&code=...`.
        state: The CSRF state sent in the login URL.

    Returns:
        The parsed query, or None if the target is malformed, is not `/callback`,
        or carries a different `state`.
    """
    try:
        split = urllib.parse.urlsplit(target)
    except ValueError:  # e.g. "Invalid IPv6 URL" for an absolute-form target.
        return None
    qs = urllib.parse.parse_qs(split.query)
    # Compare bytes: compare_digest rejects non-ASCII str input with TypeError.
    received = qs.get("state", [""])[0].encode("utf-8")
    if split.path != "/callback" or not secrets.compare_digest(received, state.encode("ascii")):
        return None
    return qs


def _callback_handler(
    server: str, state: str, result: dict[str, str]
) -> type[http.server.BaseHTTPRequestHandler]:
    """Returns a handler class that records the OAuth redirect on `/callback` into `result`.

    Args:
        server: Deployment origin; the browser is redirected to its success page.
        state: The CSRF state sent in the login URL.
        result: Receives `code` or `error` from the first valid callback.

    Returns:
        A `BaseHTTPRequestHandler` subclass for the loopback server.
    """

    class _Handler(http.server.BaseHTTPRequestHandler):
        """Handles loopback OAuth redirect callbacks on `/callback`."""

        timeout = 10.0

        def do_GET(self) -> None:
            """Validates CSRF `state` and records the authorization code or error."""
            qs = _callback_query(self.path, state)
            if qs is None:
                self.send_response(400)
                self.end_headers()
                return
            if err := qs.get("error", [""])[0]:
                result["error"] = err
                self.send_response(302)
                q = urllib.parse.urlencode({"error": err})
                self.send_header("Location", f"{server}/api/cli/success?{q}")
            elif code := qs.get("code", [""])[0]:
                result["code"] = code
                self.send_response(302)
                self.send_header("Location", f"{server}/api/cli/success")
            else:
                result["error"] = "Callback did not include an authorization code"
                self.send_response(400)
            self.end_headers()

        def log_message(self, fmt: str, *args: Any) -> None:
            del fmt, args

    return _Handler


def await_loopback_code(
    server: str,
    challenge: str,
    open_url: Callable[[str], bool],
    out: TextIO,
    timeout: float,
) -> str | None:
    """Runs the loopback callback listener and waits for the browser redirect.

    Args:
        server: Deployment origin.
        challenge: S256 PKCE code challenge.
        open_url: Opens the sign-in URL in a browser; returns False on failure.
        out: Stream for the sign-in URL.
        timeout: Seconds to wait for the callback.

    Returns:
        The authorization code, or None if the loopback server cannot start or
        the browser cannot be opened (the caller falls back to manual login).

    Raises:
        credentials.AuthError: On timeout or when the callback reports an error.
    """
    state, result = secrets.token_urlsafe(24), dict[str, str]()
    try:
        httpd = http.server.HTTPServer(("127.0.0.1", 0), _callback_handler(server, state, result))
    except OSError:  # No IPv4 loopback (e.g. a sandbox): fall back to manual login.
        return None
    with httpd:
        url = build_login_url(server, state, challenge, port=httpd.server_address[1])
        try:
            opened = bool(open_url(url))
        except Exception:  # Any browser launch failure falls back to manual login.
            opened = False
        if not opened:
            return None
        out.write(f"Opening browser to sign in to {server}:\n  {url}\n")
        out.flush()  # Show the URL before blocking, even when `out` is buffered.
        deadline = time.monotonic() + timeout
        while not result:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise credentials.AuthError("Timed out waiting for browser authentication.")
            httpd.timeout = min(remaining, 0.5)
            httpd.handle_request()
    if "error" in result:
        raise credentials.AuthError(f"Authentication failed: {result['error']}.")
    return result["code"]


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
    """POSTs `body` to `/api/cli/token` within `_TOKEN_TIMEOUT_SECONDS` of wall time.

    Args:
        server: Deployment origin.
        body: Form-encoded token request.

    Returns:
        The response body, at most `_MAX_TOKEN_RESPONSE_BYTES` + 1 bytes long.

    Raises:
        credentials.AuthError: On transport errors, an invalid URL, a non-2xx or
            redirect response, or no complete response before the deadline.
    """
    return _with_deadline(
        lambda: _send_token_request(server, body), _TOKEN_TIMEOUT_SECONDS, "Token exchange failed"
    )


def _send_token_request(server: str, body: bytes) -> bytes:
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
    try:
        opener = _build_opener(server)
        req = urllib.request.Request(
            f"{server}/api/cli/token", data=body, headers=headers, method="POST"
        )
        with opener.open(req, timeout=_TOKEN_TIMEOUT_SECONDS) as resp:
            return resp.read(_MAX_TOKEN_RESPONSE_BYTES + 1)
    except urllib.error.HTTPError as exc:
        with exc:
            detail = _format_http_error(exc)
        raise credentials.AuthError(f"Token exchange failed: {detail}.") from None
    except _RESPONSE_ERRORS as exc:
        raise credentials.AuthError(f"Token exchange failed: {exc}") from None


def _with_deadline(request: Callable[[], _T], seconds: float, failure: str) -> _T:
    """Runs `request` on a daemon thread and waits at most `seconds` for it.

    Args:
        request: The network request to run.
        seconds: Wall-clock limit.
        failure: Error message prefix, such as "Token exchange failed".

    Returns:
        The result of `request`.

    Raises:
        credentials.AuthError: If `request` does not finish in time. Exceptions
            from `request` are re-raised on the calling thread.
    """
    outcome: dict[str, Any] = {}

    def run() -> None:
        try:
            outcome["result"] = request()
        except Exception as exc:  # Re-raised on the calling thread below.
            outcome["error"] = exc

    worker = threading.Thread(target=run, name="xmc-login-request", daemon=True)
    worker.start()
    worker.join(seconds)  # An abandoned request ends with the process.
    if worker.is_alive():
        raise credentials.AuthError(f"{failure}: no complete response within {seconds:g} seconds.")
    if "error" in outcome:
        raise outcome["error"]
    return outcome["result"]


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
            oversized or malformed token responses, or a `code` or `verifier`
            that cannot be form-encoded.
    """
    form = {"grant_type": "authorization_code", "code": code, "code_verifier": verifier}
    try:
        body = urllib.parse.urlencode(form).encode("ascii")
    except UnicodeEncodeError:  # A lone surrogate from undecodable terminal input.
        raise credentials.AuthError("Token exchange failed: the code is not valid text.") from None
    raw = _post_token_request(server, body)
    if len(raw) > _MAX_TOKEN_RESPONSE_BYTES:
        raise credentials.AuthError("Token exchange failed: response too large.")
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (ValueError, RecursionError) as exc:
        raise credentials.AuthError(f"Token exchange failed: {exc}") from None
    return _validated_grant(payload)


def _is_opaque_token(value: str) -> bool:
    """Returns whether `value` is non-empty printable ASCII without whitespace.

    Bearer tokens (RFC 6750) and authorization codes have this form. Anything
    else could inject HTTP header lines or terminal control sequences.

    Args:
        value: Token or code to check.

    Returns:
        True if every character of a non-empty `value` is in `!`..`~`.
    """
    return bool(value) and all("!" <= ch <= "~" for ch in value)


def _validated_grant(p: Any) -> dict[str, Any]:
    """Returns the access token, email and lifetime from a `/api/cli/token` response.

    Args:
        p: Decoded JSON token response.

    Returns:
        Dict with the stripped `access_token` and `email`, and `expires_in`.

    Raises:
        credentials.AuthError: If `p` is not a Bearer grant with an opaque token
            (`_is_opaque_token`), a non-blank printable email, and a lifetime
            accepted by `_validated_lifetime`.
    """
    malformed = credentials.AuthError("Malformed token response from server.")
    if not isinstance(p, dict):
        raise malformed
    tok, email, token_type = p.get("access_token"), p.get("email"), p.get("token_type")
    if not isinstance(tok, str) or not isinstance(email, str) or not isinstance(token_type, str):
        raise malformed
    tok, email = tok.strip(), email.strip()
    if token_type.lower() != "bearer" or not _is_opaque_token(tok):
        raise malformed
    if not email or not email.isprintable():
        raise malformed
    return {"access_token": tok, "email": email, "expires_in": _validated_lifetime(p)}


def _validated_lifetime(p: Mapping[str, Any]) -> int:
    """Returns the `expires_in` of token response `p`.

    Args:
        p: Decoded JSON token response.

    Returns:
        The lifetime in seconds.

    Raises:
        credentials.AuthError: Unless `expires_in` is an integer (not a bool)
            above `_EXPIRY_SKEW_SECONDS` and at most `_MAX_EXPIRES_IN_SECONDS`.
    """
    exp = p.get("expires_in")
    if not isinstance(exp, int) or isinstance(exp, bool) or exp <= 0:
        raise credentials.AuthError("Malformed token response from server.")
    if exp <= _EXPIRY_SKEW_SECONDS:
        raise credentials.AuthError(
            f"Token from server is already expired: expires_in {exp} is within the"
            f" {_EXPIRY_SKEW_SECONDS}-second expiry skew."
        )
    if exp > _MAX_EXPIRES_IN_SECONDS:
        raise credentials.AuthError(
            "Malformed token response from server: expires_in exceeds"
            f" {_MAX_EXPIRES_IN_SECONDS} seconds."
        )
    return exp


def _build_opener(server: str) -> urllib.request.OpenerDirector:
    """Returns an opener that never follows redirects and never proxies loopback.

    The CLI allows plain http:// only for a loopback `server`; sending it through
    an ambient `http_proxy` would carry the authorization code and PKCE verifier
    off the machine in cleartext. Non-HTTP(S) schemes such as `file://`,
    `ftp://`, and `data:` are rejected.

    Args:
        server: Deployment origin.

    Returns:
        The opener for the probe and token requests.

    Raises:
        ValueError: If `server` is not a valid `https://` or loopback `http://` origin.
    """
    parts = urllib.parse.urlsplit(server)
    scheme = parts.scheme.lower()
    if scheme not in ("https", "http"):
        raise ValueError(f"unsupported deployment URL scheme: {scheme!r}")
    try:
        port, hostname = parts.port, parts.hostname
    except ValueError:
        raise ValueError("invalid deployment URL origin") from None
    if (
        not hostname
        or (port is not None and port < 1)
        or parts.username is not None
        or parts.password is not None
        or parts.path
        or parts.query
        or parts.fragment
    ):
        raise ValueError("invalid deployment URL origin")
    if scheme == "http" and not credentials.is_loopback_origin(server):
        raise ValueError(f"unsupported deployment URL scheme: {scheme!r}")
    opener = urllib.request.OpenerDirector()
    handlers: tuple[urllib.request.BaseHandler, ...] = (
        urllib.request.ProxyHandler({})
        if credentials.is_loopback_origin(server)
        else urllib.request.ProxyHandler(),
        urllib.request.HTTPHandler(),
        urllib.request.HTTPSHandler(),
        urllib.request.HTTPDefaultErrorHandler(),
        _NoRedirect(),
        urllib.request.HTTPErrorProcessor(),
    )
    for handler in handlers:
        opener.add_handler(handler)
    return opener


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
    """Verifies within `_PROBE_TIMEOUT_SECONDS` that `server` supports CLI login.

    Args:
        server: Deployment origin.

    Raises:
        credentials.AuthError: If `server` is unreachable, answers 2xx or 3xx,
            does not support CLI login, or sends no complete response in time.
    """
    _with_deadline(lambda: _send_probe(server), _PROBE_TIMEOUT_SECONDS, f"Cannot reach {server}")


def _send_probe(server: str) -> None:
    """Sends a bare GET to `/api/cli/login` and checks the status.

    XMC >= 0.16.0 answers a bare GET with HTTP 400 (missing parameters); it never
    returns 2xx, so a 2xx means the request reached something other than XMC
    (for example a single-page app fallback).

    Args:
        server: Deployment origin.

    Raises:
        credentials.AuthError: If `server` is unreachable, answers 2xx or 3xx, or
            does not support CLI login.
    """
    try:
        opener = _build_opener(server)
    except ValueError as exc:
        raise credentials.AuthError(f"Cannot reach deployment server: {exc}") from None
    try:
        with opener.open(f"{server}/api/cli/login", timeout=_PROBE_TIMEOUT_SECONDS) as resp:
            status = resp.status
    except urllib.error.HTTPError as exc:
        with exc:
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
    no_browser: bool,
    timeout: float,
    env: Mapping[str, str],
    open_url: Callable[[str], bool],
    prompt: Callable[[str], str],
    out: TextIO,
) -> dict[str, Any]:
    """Executes loopback or manual PKCE login and returns the token grant.

    Args:
        server: Deployment origin.
        no_browser: Whether to skip the browser loopback flow.
        timeout: Seconds to wait for the loopback callback.
        env: Environment used to detect a local browser.
        open_url: Opens the sign-in URL in a browser; returns False on failure.
        prompt: Reads the authorization code in the manual flow.
        out: Stream for the sign-in instructions.

    Returns:
        The validated token grant from `exchange_code`.

    Raises:
        credentials.AuthError: If the probe, the loopback flow, or the token
            exchange fails, or the code cannot be read or is invalid.
    """
    _probe_cli_login(server)
    if not no_browser and browser_available(env):
        verifier, challenge = pkce_pair()
        code = await_loopback_code(server, challenge, open_url, out, timeout)
        if code is not None:
            return exchange_code(server, code, verifier)
        out.write("Could not launch browser; falling back to manual login.\n")
    elif not no_browser:
        out.write("No local browser detected; using manual login.\n")
    verifier, challenge = pkce_pair()
    url = build_login_url(server, secrets.token_urlsafe(24), challenge)
    terminal_text.write(
        out,
        f"Open this URL in any browser to sign in to {server}:\n  {url}\n"
        "Never share the one-time code with anyone.\n",
    )
    # The prompt may write to /dev/tty, so a buffered `out` must show the URL first.
    out.flush()
    return exchange_code(server, _read_code(prompt), verifier)


def _read_code(prompt: Callable[[str], str]) -> str:
    """Prompts for the one-time authorization code.

    Args:
        prompt: Reads the authorization code.

    Returns:
        The code without surrounding whitespace.

    Raises:
        credentials.AuthError: If the prompt fails, no code is entered, or the
            code is not printable ASCII without spaces.
    """
    try:
        code = prompt("Enter authorization code: ").strip()
    except EOFError:  # Closed or non-interactive stdin.
        code = ""
    except _PROMPT_ERRORS as exc:
        raise credentials.AuthError(f"Cannot read the authorization code: {exc}") from None
    if not code:
        raise credentials.AuthError("No authorization code entered.")
    if not _is_opaque_token(code):
        raise credentials.AuthError(
            "Invalid authorization code: expected printable ASCII without spaces."
        )
    return code


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


def _valid_entry(entry: Any) -> dict[str, Any] | None:
    """Validates and normalizes a cached credentials entry.

    Args:
        entry: A value from the credentials cache.

    Returns:
        The entry with a stripped token and email and an integer `expires_at`, or
        None if it is not a dict, the token or email is blank, or it expires within
        `_EXPIRY_SKEW_SECONDS`.
    """
    if not isinstance(entry, dict):
        return None
    tok, email, exp = (
        entry.get("access_token"),
        entry.get("email"),
        entry.get("expires_at"),
    )
    # Credential expiry is a wall-clock Unix timestamp, not an interval.
    now = datetime.datetime.now(datetime.UTC).timestamp()
    if not (
        isinstance(tok, str)
        and tok.strip()
        and isinstance(email, str)
        and email.strip()
        and not isinstance(exp, bool)
        and (isinstance(exp, int) or (isinstance(exp, float) and math.isfinite(exp)))
        and exp > now + _EXPIRY_SKEW_SECONDS
    ):
        return None
    return {
        "access_token": tok.strip(),
        "email": email.strip(),
        "expires_at": int(exp),
    }


def _positive_seconds(raw: str) -> float:
    """Parses a `--timeout` value, which must be a positive finite number of seconds.

    Args:
        raw: The flag value.

    Returns:
        The number of seconds.

    Raises:
        argparse.ArgumentTypeError: If `raw` is not a positive finite number.
    """
    try:
        value = float(raw)
    except ValueError:
        raise argparse.ArgumentTypeError(f"invalid number: {raw!r}") from None
    if not math.isfinite(value) or value <= 0:
        raise argparse.ArgumentTypeError("must be a positive finite number")
    return value


def _add_login_flags(parser: argparse.ArgumentParser) -> None:
    """Adds the `xmc login` flags to `parser`.

    Args:
        parser: The `xmc login` argument parser.
    """
    parser.add_argument(
        "--no-browser",
        action="store_true",
        help="print the sign-in URL and prompt for the one-time code instead of opening a browser",
    )
    parser.add_argument(
        "--timeout",
        type=_positive_seconds,
        default=300.0,
        help="seconds to wait for the browser sign-in (default: %(default)s)",
    )
    group = parser.add_mutually_exclusive_group()
    group.add_argument(
        "--status", action="store_true", help="show the cached login for this deployment and exit"
    )
    group.add_argument(
        "--print-token",
        action="store_true",
        help="print the cached access token and exit; fails if not logged in",
    )


def _parse_login_args(
    argv: Sequence[str],
    out: TextIO,
    err: TextIO,
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
    _add_login_flags(parser)
    return arg_parsing.parse_args(parser, argv, out, err)


def _report_cached_login(
    args: argparse.Namespace,
    server: str,
    entry: dict[str, Any] | None,
    out: TextIO,
    err: TextIO,
) -> int:
    """Handles `--print-token` and `--status` from the cached `entry`.

    Args:
        args: Parsed flags; `print_token` or `status` is set.
        server: Deployment origin.
        entry: The valid cached entry for `server`, or None.
        out: Stream for the token or status.
        err: Stream for the not-logged-in message.

    Returns:
        0 if `entry` is valid, else 1.
    """
    if args.print_token:
        if not entry:
            err.write(f"No valid login for {server}. Run `xmc login`.\n")
            return 1
        out.write(f"{entry['access_token']}\n")
        return 0
    if not entry:
        err.write(f"Not logged in to {server}. Run `xmc login`.\n")
        return 1
    exp = _format_expiry(entry["expires_at"])
    out.write(f"Logged in to {server} as {entry['email']} until {exp}.\n")
    return 0


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
    open_url: Callable[[str], bool] = webbrowser.open,
    prompt: Callable[[str], str] = getpass.getpass,
) -> int:
    """Runs `xmc login` and caches the resulting credentials.

    With `--status` or `--print-token`, reports the cached login instead.

    Args:
        argv: Subcommand arguments after `login`.
        env: Environment used to resolve the server and credentials path;
            defaults to `os.environ`.
        out: Stream for instructions and success output; defaults to `sys.stdout`.
        err: Stream for error messages; defaults to `sys.stderr`.
        open_url: Opens the sign-in URL in a browser; defaults to `webbrowser.open`.
        prompt: Reads the authorization code; defaults to `getpass.getpass`.

    Returns:
        Process exit code: 0 on success or `--help`, 1 on authentication or
        cache errors or when not logged in, 2 on invalid arguments.
    """
    env = os.environ if env is None else env
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    parsed = _parse_login_args(argv, out, err)
    if isinstance(parsed, int):
        return parsed
    args = parsed
    try:
        server, path = credentials.resolve_server(env), credentials.credentials_path(env)
        deployments = credentials.load_credentials(path)  # Also fails fast before prompting.
        if args.print_token or args.status:
            return _report_cached_login(
                args, server, _valid_entry(deployments.get(server)), out, err
            )
        grant = _interactive_login(
            server, args.no_browser, args.timeout, env, open_url, prompt, out
        )
        expires_at = _cache_grant(path, server, grant)
        terminal_text.write(
            out,
            f"Logged in to {server} as {grant['email']} until {_format_expiry(expires_at)}.\n",
        )
        return 0
    except credentials.AuthError as exc:
        terminal_text.write(err, f"Error: {exc}\n")
        return 1
