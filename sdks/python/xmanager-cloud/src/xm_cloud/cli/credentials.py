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
"""Credentials store and deployment origin resolution for `xmc`.

Uses only the standard library, so it works without `xmanager`.
"""

from __future__ import annotations

import contextlib
import json
import os
import pathlib
import stat
import tempfile
import urllib.parse
from collections.abc import Mapping
from typing import Any

NO_SERVER_MESSAGE = (
    "No XManager on Cloud (xm_cloud) deployment configured"
    " (set XMANAGER_ENDPOINT, XMC_API_URL, XMANAGER_UI_URL, or XMC_DOMAIN)."
    " For xm_local / Vertex AI, use 'gcloud auth application-default login'"
    " instead."
)
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
_UNSPECIFIED_HOSTS = frozenset({"0.0.0.0"})
_LOCAL_HOSTS = _LOOPBACK_HOSTS | _UNSPECIFIED_HOSTS  # Same set as xdash.auth.LOCAL_HOSTS.
_RESET_HINT = "Run `xmc logout --all` to reset."


class AuthError(Exception):
    """Raised when CLI authentication or configuration fails."""


def _reject_unsafe_target(raw: str, what: str) -> None:
    """Rejects `raw` if it has whitespace, control characters, userinfo, a query, or a fragment.

    The error never echoes `raw`, which may embed credentials or tokens.
    """
    if any(ch.isspace() or ord(ch) < 0x20 or ord(ch) == 0x7F for ch in raw):
        raise AuthError(f"Invalid {what}: whitespace or control characters are not allowed.")
    if any(ch in raw for ch in "@?#"):
        raise AuthError(f"Invalid {what}: credentials, a query, or a fragment are not allowed.")


def _extract_grpc_host(endpoint: str) -> str:
    """Extracts the lowercased hostname from a gRPC target, or "" if the target is invalid."""
    target = endpoint.strip().lower()
    for prefix in ("dns:///", "dns://", "grpc://"):
        if target.startswith(prefix):
            target = target.removeprefix(prefix).rsplit("/", 1)[-1]
            break
    try:
        parts = urllib.parse.urlsplit(f"//{target}")
        port = parts.port
    except ValueError:
        return ""
    if port is not None and port < 1:
        return ""
    return parts.hostname or ""


def _strip_grpc_prefix(host: str) -> str:
    """Maps a `grpc.api.<domain>` or `grpc.<domain>` host to `<domain>`."""
    for prefix in ("grpc.api.", "grpc."):
        if host.startswith(prefix):
            return host.removeprefix(prefix)
    return host


def _origin(raw: str, *, domain: bool = False) -> str:
    """Returns the normalized `scheme://host[:port]` origin of the URL `raw`.

    The origin is the key of the credentials cache. It uses the same normalization
    as `xdash.auth`, which reads the cache: a `grpc.` or `grpc.api.` host prefix
    and a default port are dropped. Only a `domain` (XMC_DOMAIN) may omit the
    scheme, and it must not carry a path.

    Args:
        raw: Deployment URL or bare domain string.
        domain: Whether `raw` is a bare domain (scheme optional, no path allowed).

    Returns:
        Normalized `scheme://host[:port]` origin string.

    Raises:
        AuthError: If `raw` is not a valid https (or loopback http) URL, or contains
            whitespace, control characters, userinfo, a query, or a fragment.
    """
    _reject_unsafe_target(raw, "deployment server URL")
    # Echoing `raw` is safe from here on: it has no userinfo, query, or fragment.
    invalid = AuthError(f"Invalid deployment server URL: {raw!r}.")
    if "://" not in raw:
        if not domain:
            raise invalid
        raw = f"https://{raw}"
    try:
        p = urllib.parse.urlsplit(raw)
        port = p.port
    except ValueError:
        raise invalid from None
    if port is not None and port < 1:
        raise invalid
    scheme, host = p.scheme.lower(), _strip_grpc_prefix((p.hostname or "").lower())
    if scheme not in ("https", "http") or not host or host in _UNSPECIFIED_HOSTS:
        raise invalid
    if domain and p.path not in ("", "/"):
        raise invalid
    if scheme == "http" and host not in _LOOPBACK_HOSTS:
        raise AuthError(f"Deployment server must use https:// (got {raw!r}).")
    host_str = f"[{host}]" if ":" in host else host
    if port is None or (scheme, port) in (("https", 443), ("http", 80)):
        return f"{scheme}://{host_str}"
    return f"{scheme}://{host_str}:{port}"


def _endpoint_origin(endpoint: str) -> str | None:
    """Returns the origin named by XMANAGER_ENDPOINT, or None for a loopback gRPC target."""
    _reject_unsafe_target(endpoint, "XMANAGER_ENDPOINT")
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
            " http(s) URL, or loopback). Unset it or run `xmc logout --all`."
        )
    return None


def resolve_server(env: Mapping[str, str]) -> str:
    """Resolves and validates the XMC deployment origin from `env`.

    Valid values are normalized the same way as in `xdash.auth`. Unlike xdash,
    which skips an invalid variable and reads the next one, an invalid value here
    is an error, so the CLI never uses a cache key the user did not intend.

    Args:
        env: Environment mapping checked in precedence order (`XMANAGER_ENDPOINT`,
            `XMC_API_URL`, `XMANAGER_UI_URL`, `XMC_DOMAIN`).

    Returns:
        Normalized `scheme://host[:port]` deployment origin.

    Raises:
        AuthError: If no deployment is configured or the configured value is invalid.
    """
    if (ep := env.get("XMANAGER_ENDPOINT", "").strip()) and (origin := _endpoint_origin(ep)):
        return origin
    for name in ("XMC_API_URL", "XMANAGER_UI_URL"):
        if raw := env.get(name, "").strip():
            return _origin(raw)
    if raw := env.get("XMC_DOMAIN", "").strip():
        return _origin(raw, domain=True)
    raise AuthError(NO_SERVER_MESSAGE)


def credentials_path(env: Mapping[str, str]) -> pathlib.Path:
    """Returns `$XMC_CREDENTIALS_FILE` or `$XDG_CONFIG_HOME/xmc/credentials.json`.

    Args:
        env: Environment mapping to inspect for `XMC_CREDENTIALS_FILE` and
            `XDG_CONFIG_HOME` overrides. Home-directory fallback and `~` expansion
            use the process environment, matching
            `xdash.auth.get_credentials_file_path`.

    Returns:
        Absolute path to the credentials JSON file.

    Raises:
        AuthError: If no absolute override is set and the home directory cannot be resolved.
    """
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
    """Loads the `deployments` map from `path`, rejecting group/other-accessible files.

    Args:
        path: Path to the credentials JSON file.

    Returns:
        Mapping from deployment origin to credential entry, or `{}` if `path` is
        missing or has no `deployments` object. Entries whose value is not a JSON
        object are dropped.

    Raises:
        AuthError: If `path` cannot be opened or read, is not a regular file owned by
            the current user, grants any group/other permission bits, or contains
            corrupt or unsupported JSON.
    """
    raw = _read_private_file(path)
    if raw is None:
        return {}
    try:
        data = json.loads(raw)
    except (ValueError, RecursionError) as exc:
        raise AuthError(f"Corrupt credentials file {path}: {exc}. {_RESET_HINT}") from None
    version = data.get("version") if isinstance(data, dict) else None
    if isinstance(version, bool) or version != 1:
        raise AuthError(f"Unsupported credentials schema in {path}. {_RESET_HINT}")
    deployments = data.get("deployments", {})
    if not isinstance(deployments, dict):
        return {}
    return {k: v for k, v in deployments.items() if isinstance(k, str) and isinstance(v, dict)}


def _read_private_file(path: pathlib.Path) -> bytes | None:
    """Returns the contents of `path`, or None if it does not exist.

    Raises:
        AuthError: If `path` cannot be opened or read, or fails
            `_check_private_regular_file`.
    """
    # O_NONBLOCK keeps open() from blocking on a FIFO; regular files ignore it.
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
    try:
        fd = os.open(path, flags)
    except FileNotFoundError:
        return None
    except OSError as exc:
        raise AuthError(f"Refusing to read {path}: {exc}. {_RESET_HINT}") from None
    fd_closed_by_file = False
    try:
        _check_private_regular_file(os.fstat(fd), path)
        with os.fdopen(fd, "rb") as f:
            fd_closed_by_file = True
            return f.read()
    except OSError as exc:
        raise AuthError(f"Refusing to read {path}: {exc}. {_RESET_HINT}") from None
    finally:
        if not fd_closed_by_file:
            with contextlib.suppress(OSError):
                os.close(fd)


def _check_private_regular_file(st: os.stat_result, path: pathlib.Path) -> None:
    """Raises AuthError unless `st` is a regular file that only the current user can access."""
    if not stat.S_ISREG(st.st_mode):
        raise AuthError(f"Refusing to read {path} (must be a regular file). {_RESET_HINT}")
    if hasattr(os, "getuid") and st.st_uid != os.getuid():
        raise AuthError(
            f"Refusing to read {path} (must be owned by the current user). {_RESET_HINT}"
        )
    if os.name == "posix" and (stat.S_IMODE(st.st_mode) & 0o077):
        raise AuthError(
            f"Refusing to read {path} (must not be accessible by group or others;"
            f" run `chmod 600 {path}`). {_RESET_HINT}"
        )


def save_credentials(path: pathlib.Path, deployments: Mapping[str, Mapping[str, Any]]) -> None:
    """Atomically writes `deployments` to `path` with mode 0600 (last writer wins).

    Args:
        path: Target path for the credentials JSON file.
        deployments: Mapping from deployment origin to credential entry.

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
    fd_closed_by_file = False
    try:
        with os.fdopen(fd, "wb") as f:
            fd_closed_by_file = True
            f.write(payload + b"\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    except BaseException:
        if not fd_closed_by_file:
            with contextlib.suppress(OSError):
                os.close(fd)
        with contextlib.suppress(FileNotFoundError):
            os.unlink(tmp)
        raise


def credentials_file_exists(path: pathlib.Path) -> bool:
    """Returns whether `path` (or a broken symlink at `path`) exists.

    Args:
        path: Path to check.

    Returns:
        True if `path` exists or is a symlink (including dangling/loop symlinks).

    Raises:
        AuthError: If accessing `path` fails with an `OSError`.
    """
    try:
        # is_symlink() (lstat) runs first: exists() follows the link and can raise
        # EACCES for a target the user cannot reach.
        return path.is_symlink() or path.exists()
    except OSError as exc:
        raise AuthError(f"Cannot access credentials file {path}: {exc}") from None
