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
"""`xmc logout` and credentials store for the `xmc` CLI.

Uses only the standard library, so it works without `xmanager`.
"""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import pathlib
import stat
import sys
import tempfile
import urllib.parse
from collections.abc import Mapping, Sequence
from typing import Any, TextIO

NO_SERVER_MESSAGE = (
    "xmc login authenticates with an XManager on Cloud (xm_cloud) deployment"
    " (set XMANAGER_UI_URL, XMANAGER_ENDPOINT, XMC_API_URL, or XMC_DOMAIN)."
    " For xm_local / Vertex AI, use 'gcloud auth application-default login'"
    " instead."
)
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
    fd_owned = False
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise AuthError(f"Refusing to read {path} (must be a regular file). {hint}")
        if hasattr(os, "getuid") and st.st_uid != os.getuid():
            raise AuthError(f"Refusing to read {path} (must be owned by the current user). {hint}")
        if os.name == "posix" and (stat.S_IMODE(st.st_mode) & 0o077):
            raise AuthError(f"Refusing to read {path} (must be a 0600 regular file). {hint}")
        with os.fdopen(fd, "rb") as f:
            fd_owned = True
            try:
                data = json.load(f)
            except (OSError, ValueError) as exc:
                raise AuthError(f"Corrupt credentials file {path}: {exc}. {hint}") from None
    finally:
        if not fd_owned:
            with contextlib.suppress(OSError):
                os.close(fd)
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


def _credentials_file_exists(path: pathlib.Path) -> bool:
    """Returns whether `path` exists, raising `AuthError` if it cannot be checked."""
    try:
        return path.exists()
    except OSError as exc:
        raise AuthError(f"Cannot access credentials file {path}: {exc}") from None


def _parse_args(
    parser: argparse.ArgumentParser,
    argv: Sequence[str],
    out: TextIO,
    err: TextIO,
) -> argparse.Namespace | int:
    """Parses `argv` with `parser`, redirecting argparse output to `out`/`err`."""
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            return parser.parse_args(list(argv))
    except SystemExit as exc:
        return exc.code if isinstance(exc.code, int) else 1


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
    parsed = _parse_args(parser, argv, out, err)
    if isinstance(parsed, int):
        return parsed
    args = parsed
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


def logout_command(argv: Sequence[str]) -> None:
    """Runs `logout` from a full `<program> logout ...` argv and exits on failure.

    Args:
        argv: The full command line, including the program and command names.
    """
    if code := run_logout(argv[2:]):
        sys.exit(code)
