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
"""`xmc login` and `xmc logout` command implementations.

Both commands use only the standard library, so they work without `xmanager`.
"""

from __future__ import annotations

import argparse
import base64
import contextlib
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
import stat
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser
from collections.abc import Callable, Mapping, Sequence
from typing import Any, TextIO

NO_SERVER_MESSAGE = (
    "xmc login authenticates with an XManager on Cloud (xm_cloud) deployment"
    " (set XMANAGER_UI_URL, XMANAGER_ENDPOINT, XMC_API_URL, or XMC_DOMAIN)."
    " For xm_local / Vertex AI, use 'gcloud auth application-default login'"
    " instead."
)
_EXPIRY_SKEW_SECONDS = 60
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
_LOCAL_HOSTS = _LOOPBACK_HOSTS | {"0.0.0.0"}


class AuthError(Exception):
    """Raised when CLI authentication or configuration fails."""


def _extract_grpc_host(endpoint: str) -> str:
    """Extracts the lowercased hostname from a gRPC target string."""
    target = endpoint.strip().lower()
    for prefix in ("dns:///", "dns://", "grpc://"):
        if target.startswith(prefix):
            target = target.removeprefix(prefix).rsplit("/", 1)[-1]
            break
    try:
        return urllib.parse.urlsplit(f"//{target}").hostname or ""
    except ValueError:
        return ""


def _strip_grpc_prefix(host: str) -> str:
    """Maps a `grpc.api.<domain>` or `grpc.<domain>` host to `<domain>`."""
    for prefix in ("grpc.api.", "grpc."):
        if host.startswith(prefix):
            return host.removeprefix(prefix)
    return host


def _origin(raw: str, *, domain: bool = False) -> str:
    """Returns the normalized `scheme://host[:port]` origin of the URL `raw`.

    The origin is the key of the credentials cache. It follows the same rules as
    `xdash.auth`, which reads the cache: a `grpc.` or `grpc.api.` host prefix and
    a default port are dropped. Only a `domain` (XMC_DOMAIN) may omit the scheme,
    and it must not carry a path.

    Raises:
        AuthError: If `raw` is not a valid, userinfo-free https (or loopback http) URL.
    """
    invalid = AuthError(f"Invalid deployment server URL: {raw!r}.")
    if "://" not in raw:
        if not domain:
            raise invalid
        raw = f"https://{raw}"
    allow_path = not domain
    try:
        p = urllib.parse.urlsplit(raw)
        port = p.port
    except ValueError:
        raise invalid from None
    scheme, host = p.scheme.lower(), _strip_grpc_prefix((p.hostname or "").lower())
    if scheme not in ("https", "http") or not host:
        raise invalid
    if any((p.username, p.password, p.query, p.fragment)):
        raise invalid
    if (not allow_path and p.path not in ("", "/")) or host in _LOCAL_HOSTS - _LOOPBACK_HOSTS:
        raise invalid
    if scheme == "http" and host not in _LOOPBACK_HOSTS:
        raise AuthError(f"Deployment server must use https:// (got {raw!r}).")
    host_str = f"[{host}]" if ":" in host else host
    if port is None or (scheme, port) in (("https", 443), ("http", 80)):
        return f"{scheme}://{host_str}"
    return f"{scheme}://{host_str}:{port}"


def _endpoint_origin(endpoint: str) -> str | None:
    """Returns the origin named by XMANAGER_ENDPOINT, or None for a loopback gRPC target."""
    if endpoint.lower().startswith(("http://", "https://")):
        return _origin(endpoint)
    host = _extract_grpc_host(endpoint)
    if host.startswith("grpc."):
        domain = _strip_grpc_prefix(host)
        if not domain or domain in _LOCAL_HOSTS:
            raise AuthError(f"Invalid XMANAGER_ENDPOINT: {endpoint!r}.")
        return f"https://{domain}"
    if host not in _LOOPBACK_HOSTS:
        raise AuthError(
            f"Unsupported XMANAGER_ENDPOINT {endpoint!r} (expected grpc.<domain>, an"
            " http(s) URL, or loopback)."
        )
    return None


def resolve_server(env: Mapping[str, str]) -> str:
    """Resolves and validates the XMC deployment origin from `env`."""
    if (ep := env.get("XMANAGER_ENDPOINT", "").strip()) and (origin := _endpoint_origin(ep)):
        return origin
    for name in ("XMC_API_URL", "XMANAGER_UI_URL"):
        if raw := env.get(name, "").strip():
            return _origin(raw)
    if raw := env.get("XMC_DOMAIN", "").strip():
        return _origin(raw, domain=True)
    raise AuthError(NO_SERVER_MESSAGE)


def credentials_path(env: Mapping[str, str]) -> pathlib.Path:
    """Returns `$XMC_CREDENTIALS_FILE` or `$XDG_CONFIG_HOME/xmc/credentials.json`."""
    custom = os.path.expanduser(env.get("XMC_CREDENTIALS_FILE", "").strip())
    if os.path.isabs(custom):
        return pathlib.Path(custom)
    xdg = os.path.expanduser(env.get("XDG_CONFIG_HOME", "").strip())
    if os.path.isabs(xdg):
        return pathlib.Path(xdg) / "xmc" / "credentials.json"
    try:
        home = pathlib.Path.home()
    except RuntimeError:  # No $HOME and no passwd entry, e.g. an arbitrary container UID.
        raise AuthError(
            "Cannot determine the home directory; set XMC_CREDENTIALS_FILE to an absolute path."
        ) from None
    return home / ".config" / "xmc" / "credentials.json"


def load_credentials(path: pathlib.Path) -> dict[str, dict[str, Any]]:
    """Loads the `deployments` map from `path`, verifying 0600 permissions."""
    hint = "Run `xmc logout --all` to reset."
    try:
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except FileNotFoundError:
        return {}
    except OSError as exc:
        raise AuthError(f"Refusing to read {path}: {exc}. {hint}") from None
    with os.fdopen(fd, "rb") as f:
        st = os.fstat(f.fileno())
        if os.name == "posix" and (
            not stat.S_ISREG(st.st_mode) or (stat.S_IMODE(st.st_mode) & 0o077)
        ):
            raise AuthError(f"Refusing to read {path} (must be a 0600 regular file). {hint}")
        try:
            data = json.load(f)
        except (OSError, ValueError) as exc:
            raise AuthError(f"Corrupt credentials file {path}: {exc}. {hint}") from None
    if not isinstance(data, dict) or data.get("version") != 1:
        raise AuthError(f"Unsupported credentials schema in {path}. {hint}")
    deps = data.get("deployments", {})
    return dict(deps) if isinstance(deps, dict) else {}


def save_credentials(path: pathlib.Path, deployments: Mapping[str, Mapping[str, Any]]) -> None:
    """Atomically writes `deployments` to `path` with mode 0600 (last writer wins).

    Raises:
        AuthError: If the credentials file or its directory cannot be written.
    """
    try:
        _write_credentials(path, deployments)
    except OSError as exc:
        raise AuthError(f"Cannot write credentials file {path}: {exc}") from None


def _write_credentials(path: pathlib.Path, deployments: Mapping[str, Mapping[str, Any]]) -> None:
    # Only a directory created here is restricted to 0700 (mkdir's mode is
    # subject to the umask). Existing directories, such as $HOME, /tmp, or a
    # group-shared directory named by XMC_CREDENTIALS_FILE, keep their mode.
    if not path.parent.is_dir():
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if os.name == "posix":
            os.chmod(path.parent, 0o700)
    payload = json.dumps(
        {"version": 1, "deployments": dict(deployments)}, indent=2, sort_keys=True
    ).encode("utf-8")
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=".credentials.")
    fd_owned = True
    try:
        with os.fdopen(fd, "wb") as f:
            fd_owned = False
            f.write(payload + b"\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    except BaseException:
        if fd_owned:
            with contextlib.suppress(OSError):
                os.close(fd)
        with contextlib.suppress(FileNotFoundError):
            os.unlink(tmp)
        raise


def pkce_pair() -> tuple[str, str]:
    """Returns an RFC 7636 `(code_verifier, S256 code_challenge)` pair."""
    verifier = secrets.token_urlsafe(32)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return verifier, base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def browser_available(env: Mapping[str, str]) -> bool:
    """Returns True when a local GUI browser can likely be launched."""
    if any(env.get(k) for k in ("SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY")):
        return False
    return sys.platform in ("darwin", "win32") or bool(
        env.get("DISPLAY") or env.get("WAYLAND_DISPLAY")
    )


def build_login_url(server: str, state: str, challenge: str, port: int | None = None) -> str:
    """Builds the `/api/cli/login` URL for loopback (`port`) or manual mode."""
    params = [("port", str(port))] if port is not None else [("mode", "manual")]
    params += [
        ("state", state),
        ("code_challenge", challenge),
        ("code_challenge_method", "S256"),
    ]
    return f"{server}/api/cli/login?{urllib.parse.urlencode(params)}"


def _callback_handler(
    server: str, state: str, result: dict[str, str]
) -> type[http.server.BaseHTTPRequestHandler]:
    """Returns a handler class that records the OAuth redirect on `/callback` into `result`."""

    class _Handler(http.server.BaseHTTPRequestHandler):
        """Handles loopback OAuth redirect callbacks on `/callback`."""

        def do_GET(self) -> None:
            """Validates CSRF `state` and records the authorization code or error."""
            split = urllib.parse.urlsplit(self.path)
            qs = urllib.parse.parse_qs(split.query)
            # Compare bytes: compare_digest rejects non-ASCII str input with TypeError.
            received = qs.get("state", [""])[0].encode("utf-8")
            if split.path != "/callback" or not secrets.compare_digest(
                received, state.encode("ascii")
            ):
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
    """Runs the loopback callback listener; returns `None` if unopenable."""
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
        deadline = time.monotonic() + timeout
        while not result:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AuthError("Timed out waiting for browser authentication.")
            httpd.timeout = min(remaining, 0.5)
            httpd.handle_request()
    if "error" in result:
        raise AuthError(f"Authentication failed: {result['error']}.")
    return result["code"]


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
        raise AuthError(f"Token exchange failed: {_format_http_error(exc)}.") from None
    except (OSError, ValueError, http.client.HTTPException) as exc:
        raise AuthError(f"Token exchange failed: {exc}") from None
    return _validated_grant(payload)


def _validated_grant(p: Any) -> dict[str, Any]:
    """Returns the access token, email and lifetime from a `/api/cli/token` response."""
    if not isinstance(p, dict):
        raise AuthError("Malformed token response from server.")
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
        raise AuthError("Malformed token response from server.")
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
            raise AuthError(
                f"Server {server} does not support `xmc login` (HTTP {exc.code};"
                " requires XMC >= 0.16.0). Use XMC_CLIENT_SA + IAP_CLIENT_ID or"
                " XMC_AUTH_TOKEN, or upgrade the XMC backend."
            ) from None
        raise AuthError(f"Server {server} returned HTTP {exc.code} on `/api/cli/login`.") from None
    except (OSError, ValueError, http.client.HTTPException) as exc:  # ValueError: bad IDNA host.
        raise AuthError(f"Cannot reach {server}: {exc}") from None


def _interactive_login(
    server: str,
    no_browser: bool,
    timeout: float,
    env: Mapping[str, str],
    open_url: Callable[[str], bool],
    prompt: Callable[[str], str],
    out: TextIO,
) -> dict[str, Any]:
    """Executes loopback or manual PKCE login and returns the token grant."""
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
    out.write(
        f"Open this URL in any browser to sign in to {server}:\n  {url}\n"
        "Never share the one-time code with anyone.\n"
    )
    try:
        code = prompt("Enter authorization code: ").strip()
    except EOFError:  # Closed or non-interactive stdin.
        code = ""
    if not code:
        raise AuthError("No authorization code entered.")
    return exchange_code(server, code, verifier)


def _format_expiry(expires_at: int) -> str:
    """Formats `expires_at` in local time, or as a raw timestamp when out of range."""
    try:
        utc = datetime.datetime.fromtimestamp(expires_at, datetime.UTC)
    except (OverflowError, OSError, ValueError):
        return f"Unix time {expires_at}"
    return utc.astimezone().strftime("%Y-%m-%d %H:%M %Z")


def _valid_entry(entry: Any) -> dict[str, Any] | None:
    """Returns normalized `entry` if it has a valid non-expired token and email."""
    if not isinstance(entry, dict):
        return None
    tok, email, exp = (
        entry.get("access_token"),
        entry.get("email"),
        entry.get("expires_at"),
    )
    if not (
        isinstance(tok, str)
        and tok.strip()
        and isinstance(email, str)
        and email.strip()
        and not isinstance(exp, bool)
        and isinstance(exp, (int, float))
        and math.isfinite(exp)
        # Credential expiry is a wall-clock Unix timestamp, not an interval.
        and exp > time.time() + _EXPIRY_SKEW_SECONDS  # noqa: TID251
    ):
        return None
    return {
        "access_token": tok.strip(),
        "email": email.strip(),
        "expires_at": int(exp),
    }


def _parse_login_args(argv: Sequence[str]) -> argparse.Namespace:
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
    parser.add_argument("--no-browser", action="store_true")
    parser.add_argument("--timeout", type=float, default=300.0)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--status", action="store_true")
    group.add_argument("--print-token", action="store_true")
    args = parser.parse_args(list(argv))
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("--timeout must be a positive finite number")
    return args


def _report_cached_login(
    args: argparse.Namespace,
    server: str,
    entry: dict[str, Any] | None,
    out: TextIO,
    err: TextIO,
) -> int:
    """Handles `--print-token` and `--status` from the cached `entry`."""
    if args.print_token:
        if not entry:
            err.write(f"No valid login for {server}. Run `xmc login`.\n")
            return 1
        out.write(f"{entry['access_token']}\n")
        return 0
    if not entry:
        out.write(f"Not logged in to {server}. Run `xmc login`.\n")
        return 1
    exp = _format_expiry(entry["expires_at"])
    out.write(f"Logged in to {server} as {entry['email']} until {exp}.\n")
    return 0


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
    save_credentials(path, deployments)
    return expires_at


def run_login(
    argv: Sequence[str],
    env: Mapping[str, str] = os.environ,
    out: TextIO = sys.stdout,
    err: TextIO = sys.stderr,
    open_url: Callable[[str], bool] = webbrowser.open,
    prompt: Callable[[str], str] = getpass.getpass,
) -> int:
    """Parses `xmc login` flags, executes the action, and returns exit code."""
    args = _parse_login_args(argv)
    try:
        server, path = resolve_server(env), credentials_path(env)
        deployments = load_credentials(path)
        if args.print_token or args.status:
            return _report_cached_login(
                args, server, _valid_entry(deployments.get(server)), out, err
            )
        grant = _interactive_login(
            server, args.no_browser, args.timeout, env, open_url, prompt, out
        )
        expires_at = _cache_grant(path, deployments, server, grant)
        out.write(
            f"Logged in to {server} as {grant['email']} until {_format_expiry(expires_at)}.\n"
        )
        return 0
    except AuthError as exc:
        err.write(f"Error: {exc}\n")
        return 1


def _credentials_file_exists(path: pathlib.Path) -> bool:
    """Returns whether `path` exists, raising `AuthError` if it cannot be checked."""
    try:
        return path.exists()
    except OSError as exc:
        raise AuthError(f"Cannot access credentials file {path}: {exc}") from None


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
    args = parser.parse_args(list(argv))
    try:
        path = credentials_path(env)
        if args.all:
            if _credentials_file_exists(path):
                save_credentials(path, {})
            out.write("Logged out of all XMC deployments.\n")
            return 0
        server = resolve_server(env)
        deployments = load_credentials(path)
        if server in deployments:
            del deployments[server]
            save_credentials(path, deployments)
            out.write(f"Logged out of {server}.\n")
        else:
            out.write(f"Not logged in to {server}.\n")
        return 0
    except AuthError as exc:
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


def logout_command(argv: Sequence[str]) -> None:
    """Runs `logout` from a full `<program> logout ...` argv and exits on failure.

    Args:
        argv: The full command line, including the program and command names.
    """
    if code := run_logout(argv[2:]):
        sys.exit(code)
