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
import ipaddress
import json
import os
import pathlib
import stat
import tempfile
import urllib.parse
from collections.abc import Mapping
from typing import Any, BinaryIO

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
_EXPECTED_URL = (
    "expected https://<host>[:<port>] with a port in 1-65535 (http:// only for a loopback host)"
)
# A cached entry is a few KiB, so 1 MiB holds hundreds of deployments while
# keeping a hand-edited or misnamed file from exhausting memory.
_MAX_CREDENTIALS_BYTES = 1 << 20


class AuthError(Exception):
    """Raised when CLI authentication or configuration fails."""


def _reject_unsafe_target(raw: str, what: str) -> None:
    """Rejects `raw` if it has unsafe characters, userinfo, a query, or a fragment.

    Unsafe characters are whitespace and non-printable characters: C0/C1 control
    characters, Unicode format characters such as bidi overrides, and the lone
    surrogates that stand for undecodable bytes in `os.environ`. The error never
    echoes `raw`, which may embed credentials or tokens.

    Args:
        raw: Deployment URL, domain, or gRPC target read from the environment.
        what: Name of the value, used in the error message.

    Raises:
        AuthError: If `raw` is unsafe.
    """
    if any(ch.isspace() or not ch.isprintable() for ch in raw):
        raise AuthError(f"Invalid {what}: whitespace or non-printable characters are not allowed.")
    if any(ch in raw for ch in "@?#"):
        raise AuthError(f"Invalid {what}: credentials, a query, or a fragment are not allowed.")


def _strip_grpc_scheme(target: str) -> str | None:
    """Removes a `dns:///`, `dns://<authority>/`, or `grpc://` prefix from `target`.

    Args:
        target: Lowercased gRPC target.

    Returns:
        `target` without its prefix (any later `/` is kept, so the caller sees the
        path), or None for a `dns://<authority>` target without a `/`.
    """
    for prefix in ("dns:///", "grpc://"):
        if target.startswith(prefix):
            return target.removeprefix(prefix)
    if target.startswith("dns://"):
        _, slash, rest = target.removeprefix("dns://").partition("/")
        return rest if slash else None
    return target


def _endpoint_error(problem: str) -> AuthError:
    """Returns the error for an invalid XMANAGER_ENDPOINT, without echoing its value.

    Args:
        problem: What is wrong with the value; must not contain the raw value.

    Returns:
        The `AuthError` to raise.
    """
    return AuthError(f"Invalid XMANAGER_ENDPOINT: {problem}. Unset it or run `xmc logout --all`.")


def _extract_grpc_host(endpoint: str) -> str:
    """Extracts the lowercased hostname from a gRPC target.

    Args:
        endpoint: `XMANAGER_ENDPOINT` value, optionally `dns:///`, `dns://<authority>/`,
            or `grpc://` prefixed.

    Returns:
        The hostname; an unbracketed IPv6 address (`::1`) is accepted, as in xdash.

    Raises:
        AuthError: If the target is malformed, has an invalid port, or has a path.
    """
    target = _strip_grpc_scheme(endpoint.strip().lower())
    if target is None:
        raise _endpoint_error("a dns://<authority> target needs a `/` before the host")
    if target.count(":") > 1 and not target.startswith("["):
        if _is_ipv6_address(target):
            return target
        raise _endpoint_error("malformed host or port")
    try:
        parts = urllib.parse.urlsplit(f"//{target}")
        port = parts.port
    except ValueError:  # A non-numeric port or one above 65535.
        raise _endpoint_error("malformed host, or a port outside 1-65535") from None
    if parts.path:
        raise _endpoint_error("a gRPC target must not have a path")
    if (port is not None and port < 1) or not parts.hostname:
        raise _endpoint_error("missing host, or a port outside 1-65535")
    return parts.hostname


def _is_ipv6_address(text: str) -> bool:
    """Returns whether `text` is an IPv6 address literal without brackets or a port."""
    try:
        return isinstance(ipaddress.ip_address(text), ipaddress.IPv6Address)
    except ValueError:
        return False


def _strip_grpc_prefix(host: str) -> str:
    """Maps a `grpc.api.<domain>` or `grpc.<domain>` host to `<domain>`."""
    for prefix in ("grpc.api.", "grpc."):
        if host.startswith(prefix):
            return host.removeprefix(prefix)
    return host


def _split_url(raw: str, name: str) -> tuple[str, str, int | None, str]:
    """Splits the deployment URL `raw` into its scheme, host, port, and path.

    Args:
        raw: Deployment URL that passed `_reject_unsafe_target`.
        name: Name of the environment variable, used in the error message.

    Returns:
        The lowercased `http`/`https` scheme, the lowercased host without brackets
        or a `grpc.`/`grpc.api.` prefix, the port (None if absent), and the path.

    Raises:
        AuthError: If `raw` lacks a supported scheme or a host, names the
            unspecified host, or has a port outside 1-65535. Never echoes `raw`.
    """
    invalid = AuthError(f"Invalid {name}: {_EXPECTED_URL}.")
    if "://" not in raw:
        raise invalid
    try:
        p = urllib.parse.urlsplit(raw)
        port = p.port
    except ValueError:
        raise invalid from None
    scheme, host = p.scheme.lower(), _strip_grpc_prefix((p.hostname or "").lower())
    if (port is not None and port < 1) or scheme not in ("https", "http"):
        raise invalid
    if not host or host in _UNSPECIFIED_HOSTS:
        raise invalid
    return scheme, host, port, p.path


def _origin(raw: str, name: str, *, domain: bool = False) -> str:
    """Returns the normalized `scheme://host[:port]` origin of the URL `raw`.

    The origin is the cache key, normalized as in `xdash.auth` (no `grpc.` or
    `grpc.api.` host prefix, no default port). Errors echo at most the origin,
    never `raw`, whose path may carry a token.

    Args:
        raw: Deployment URL, or a bare domain if `domain` is set.
        name: Name of the environment variable, used in error messages.
        domain: Whether `raw` is an XMC_DOMAIN value (scheme optional, no path).

    Returns:
        Normalized `scheme://host[:port]` origin string.

    Raises:
        AuthError: If `raw` is unsafe or not a valid https (or loopback http) URL.
    """
    _reject_unsafe_target(raw, name)
    if domain and "://" not in raw:
        raw = f"https://{raw}"
    scheme, host, port, path = _split_url(raw, name)
    if domain and path not in ("", "/"):
        raise AuthError(f"Invalid {name}: a domain must not have a path.")
    host_str = f"[{host}]" if ":" in host else host
    default_port = 443 if scheme == "https" else 80
    origin = f"{scheme}://{host_str}" + ("" if port in (None, default_port) else f":{port}")
    if scheme == "http" and host not in _LOOPBACK_HOSTS:
        raise AuthError(f"Invalid {name}: {origin} must use https:// (http:// is loopback only).")
    return origin


def _endpoint_origin(endpoint: str) -> str | None:
    """Returns the deployment origin named by an XMANAGER_ENDPOINT value.

    Args:
        endpoint: Stripped, non-empty `XMANAGER_ENDPOINT` value: an http(s) URL, a
            `grpc.<domain>` or `grpc.api.<domain>` gRPC target, or a loopback gRPC
            target.

    Returns:
        The origin, or None for a loopback gRPC target (the caller then reads the
        next variable).

    Raises:
        AuthError: If the value is unsafe or malformed, or is a gRPC target that is
            neither `grpc.`-prefixed nor loopback. Errors echo at most the host.
    """
    _reject_unsafe_target(endpoint, "XMANAGER_ENDPOINT")
    if endpoint.lower().startswith(("http://", "https://")):
        return _origin(endpoint, "XMANAGER_ENDPOINT")
    host = _extract_grpc_host(endpoint)
    if host.startswith("grpc."):
        domain = _strip_grpc_prefix(host)
        if not domain or domain in _LOCAL_HOSTS:
            raise _endpoint_error(f"{host} does not name a deployment")
        return f"https://{domain}"
    if host not in _LOOPBACK_HOSTS:
        raise _endpoint_error(
            f"expected grpc.<domain>, an http(s) URL, or a loopback target (got host {host})"
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
            return _origin(raw, name)
    if raw := env.get("XMC_DOMAIN", "").strip():
        return _origin(raw, "XMC_DOMAIN", domain=True)
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
        AuthError: If no absolute override is set and the home directory cannot be
            resolved or is relative, or if the path contains a NUL character.
    """
    path = _configured_credentials_path(env) or _home_credentials_path()
    if "\x00" in str(path):
        raise AuthError(
            "Invalid credentials path: XMC_CREDENTIALS_FILE, XDG_CONFIG_HOME, and HOME"
            " must not contain NUL characters."
        )
    return path


def _configured_credentials_path(env: Mapping[str, str]) -> pathlib.Path | None:
    """Returns the path named by an absolute `XMC_CREDENTIALS_FILE` or `XDG_CONFIG_HOME`.

    Args:
        env: Environment mapping to inspect.

    Returns:
        The credentials path, or None if neither variable is an absolute path.
    """
    custom = os.path.expanduser(env.get("XMC_CREDENTIALS_FILE", "").strip())
    if os.path.isabs(custom):
        return pathlib.Path(custom)
    xdg = os.path.expanduser(env.get("XDG_CONFIG_HOME", "").strip())
    if os.path.isabs(xdg):
        return pathlib.Path(xdg) / "xmc" / "credentials.json"
    return None


def _home_credentials_path() -> pathlib.Path:
    """Returns `~/.config/xmc/credentials.json`.

    Returns:
        The default credentials path under the absolute home directory.

    Raises:
        AuthError: If the home directory cannot be resolved or is a relative path,
            which would put the credentials in the working directory.
    """
    hint = "set XMC_CREDENTIALS_FILE to an absolute path."
    try:
        home = pathlib.Path.home()
    except RuntimeError:  # No $HOME and no passwd entry, e.g. an arbitrary container UID.
        raise AuthError(f"Cannot determine the home directory; {hint}") from None
    if not home.is_absolute():
        raise AuthError(f"The home directory ($HOME) is a relative path; {hint}")
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
    """Returns the contents of the credentials file `path`.

    Args:
        path: Path of the credentials file.

    Returns:
        The file contents, or None if `path` does not exist.

    Raises:
        AuthError: If `path` cannot be opened or read, is larger than
            `_MAX_CREDENTIALS_BYTES`, or fails `_check_private_regular_file`.
    """
    fd = _open_without_following(path)
    if fd is None:
        return None
    fd_closed_by_file = False
    try:
        _check_private_regular_file(os.fstat(fd), path)
        with os.fdopen(fd, "rb") as f:
            fd_closed_by_file = True
            return _read_bounded(f, path)
    except OSError as exc:
        raise AuthError(f"Refusing to read {path}: {exc}. {_RESET_HINT}") from None
    finally:
        if not fd_closed_by_file:
            with contextlib.suppress(OSError):
                os.close(fd)


def _open_without_following(path: pathlib.Path) -> int | None:
    """Opens `path` read-only without following a symlink or blocking on a FIFO.

    Args:
        path: Path of the credentials file.

    Returns:
        The file descriptor, or None if `path` does not exist.

    Raises:
        AuthError: If `path` cannot be opened or contains a NUL character.
    """
    # O_NONBLOCK keeps open() from blocking on a FIFO; regular files ignore it.
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
    try:
        return os.open(path, flags)
    except FileNotFoundError:
        return None
    except (OSError, ValueError) as exc:  # ValueError: a NUL character in `path`.
        raise AuthError(f"Refusing to read {path}: {exc}. {_RESET_HINT}") from None


def _read_bounded(f: BinaryIO, path: pathlib.Path) -> bytes:
    """Reads `f`, refusing more than `_MAX_CREDENTIALS_BYTES`.

    Args:
        f: Open credentials file.
        path: Path of `f`, used in the error message.

    Returns:
        The contents of `f`.

    Raises:
        AuthError: If `f` holds more than `_MAX_CREDENTIALS_BYTES`.
    """
    data = f.read(_MAX_CREDENTIALS_BYTES + 1)
    if len(data) > _MAX_CREDENTIALS_BYTES:
        raise AuthError(
            f"Refusing to read {path} (larger than {_MAX_CREDENTIALS_BYTES} bytes). {_RESET_HINT}"
        )
    return data


def _check_private_regular_file(st: os.stat_result, path: pathlib.Path) -> None:
    """Checks that `st` is a regular file that only the current user can access.

    Args:
        st: `fstat` result of the opened credentials file.
        path: Path of the file, used in error messages.

    Raises:
        AuthError: If the file is not regular, is owned by another user, or grants
            any group/other permission bit (POSIX only).
    """
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
    # ValueError: a NUL character in `path`. RecursionError: a deeply nested entry
    # that json.loads accepted but the pure-Python encoder (`indent` on Python
    # 3.11/3.12) cannot serialize.
    except (OSError, ValueError, RecursionError) as exc:
        raise AuthError(f"Cannot write credentials file {path}: {exc}") from None


def _ensure_parent_dir(path: pathlib.Path) -> None:
    """Creates every missing ancestor directory of `path` with mode 0700.

    Each directory created here is restricted to 0700 regardless of the umask.
    Existing directories, such as $HOME, /tmp, or a group-shared directory named
    by XMC_CREDENTIALS_FILE, keep their mode.

    Args:
        path: Path of the credentials file.

    Raises:
        OSError: If a directory cannot be created or restricted.
        ValueError: If `path` contains a NUL character.
    """
    missing = []
    directory = path.parent
    while not directory.is_dir() and directory != directory.parent:
        missing.append(directory)
        directory = directory.parent
    for directory in reversed(missing):
        _make_private_dir(directory)


def _make_private_dir(directory: pathlib.Path) -> None:
    """Creates `directory` with mode 0700, unless another process just created it.

    Args:
        directory: Directory whose parent exists.

    Raises:
        OSError: If `directory` cannot be created or restricted.
    """
    try:
        directory.mkdir(mode=0o700)
    except FileExistsError:
        return  # Created concurrently: not ours to restrict.
    if os.name == "posix":
        os.chmod(directory, 0o700)  # mkdir's mode is subject to the umask.


def _write_credentials(path: pathlib.Path, deployments: Mapping[str, Mapping[str, Any]]) -> None:
    """Writes `deployments` to a temporary file and atomically renames it to `path`.

    Args:
        path: Target path for the credentials JSON file.
        deployments: Mapping from deployment origin to credential entry.

    Raises:
        OSError: If a file system call fails; the temporary file is removed.
    """
    _ensure_parent_dir(path)
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
        with contextlib.suppress(OSError):
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
