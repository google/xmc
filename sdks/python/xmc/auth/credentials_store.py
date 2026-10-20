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

"""Owner-only on-disk cache of CLI tokens, keyed by deployment origin.

Stable v1 schema, which other clients may read directly::

    {"version": 1, "deployments": {"https://<host>[:<port>]": {"server": <same origin>,
        "access_token": "...", "email": "...", "expires_at": <unix seconds>}}}
"""

from __future__ import annotations

import dataclasses
import json
import os
import secrets
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any, TextIO

from xmc.auth.errors import AuthError

CACHE_VERSION = 1
# Tokens this close to expiry count as expired so they don't lapse mid-command.
EXPIRY_MARGIN_SECONDS = 60
_IS_POSIX = os.name == "posix"


@dataclasses.dataclass(frozen=True)
class StoredCredential:
    """A cached CLI token for one deployment; `expires_at` is in Unix seconds."""

    server: str
    access_token: str
    email: str
    expires_at: int

    def is_expired(self, now: float) -> bool:
        """Reports whether the token expires within EXPIRY_MARGIN_SECONDS of `now`."""
        return now + EXPIRY_MARGIN_SECONDS >= self.expires_at

    @classmethod
    def from_json(cls, entry: Any) -> StoredCredential | None:
        """Parses one cache entry, returning None if it is malformed."""
        if not isinstance(entry, dict):
            return None
        values: list[Any] = [entry.get(f.name) for f in dataclasses.fields(cls)]
        *strings, expires_at = values
        if not all(isinstance(s, str) and s for s in strings) or type(expires_at) is not int:
            return None
        return cls(*values)


def default_credentials_path(env: Mapping[str, str]) -> Path:
    """Returns `$XDG_CONFIG_HOME/xmc/credentials.json`, defaulting to `~/.config`."""
    xdg = env.get("XDG_CONFIG_HOME", "")
    base = Path(xdg) if os.path.isabs(xdg) else Path.home() / ".config"
    return base / "xmc" / "credentials.json"


def _matching(origin: str, entry: Any) -> StoredCredential | None:
    """Parses a cache entry, accepting it only if it was issued by `origin`."""
    credential = StoredCredential.from_json(entry)
    return credential if credential and credential.server == origin else None


class CredentialsStore:
    """Reads and atomically writes the owner-only credentials cache."""

    def __init__(self, path: Path, warnings: TextIO = sys.stderr) -> None:
        """Initializes the store at `path`, reporting unreadable files to `warnings`."""
        self._path = path
        self._warnings = warnings

    def get(self, origin: str) -> StoredCredential | None:
        """Returns the possibly expired credential issued by exactly this origin."""
        return _matching(origin, self._read().get(origin))

    def servers(self) -> list[str]:
        """Returns the origins of all well-formed cached credentials."""
        return [origin for origin, entry in self._read().items() if _matching(origin, entry)]

    def put(self, credential: StoredCredential) -> None:
        """Stores or replaces the credential for `credential.server`.

        Raises:
            AuthError: If the cache cannot be written; the token is then lost.
        """
        self._write({**self._read(), credential.server: dataclasses.asdict(credential)})

    def remove(self, origin: str) -> None:
        """Deletes a deployment's credential, if present.

        Raises:
            AuthError: If the cache cannot be written.
        """
        deployments = self._read()
        if deployments.pop(origin, None) is not None:
            self._write(deployments)

    def _read(self) -> dict[str, Any]:
        """Loads the deployments map; unreadable, corrupt, or unknown-version files read as empty."""
        try:
            raw = self._path.read_text(encoding="utf-8")
        except FileNotFoundError:
            return {}
        except OSError as e:
            return self._ignore(e.strerror or str(e))
        try:
            data = json.loads(raw)
        except ValueError:
            return self._ignore("it is not valid JSON")
        valid = isinstance(data, dict) and data.get("version") == CACHE_VERSION
        deployments = data.get("deployments") if valid else None
        return dict(deployments) if isinstance(deployments, dict) else {}

    def _ignore(self, reason: str) -> dict[str, Any]:
        """Warns that the cache is unreadable and returns an empty map."""
        self._warnings.write(f"Warning: ignoring unreadable {self._path}: {reason}.\n")
        return {}

    def _write(self, deployments: dict[str, Any]) -> None:
        """Atomically replaces the cache, mapping failures to an actionable AuthError."""
        try:
            self._replace_file(deployments)
        except OSError as e:
            raise AuthError(
                f"Could not write the credentials cache {self._path} ({e.strerror or e}). "
                f"Make {self._path.parent} writable by you only, then run `xmc login` again."
            ) from None

    def _replace_file(self, deployments: dict[str, Any]) -> None:
        """Writes an owner-only (0600) temp file in a 0700 directory, then renames it."""
        directory = self._path.parent
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        if _IS_POSIX:
            directory.chmod(0o700)
        tmp = directory / f".{self._path.name}.{secrets.token_hex(8)}.tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                json.dump({"version": CACHE_VERSION, "deployments": deployments}, f, indent=2)
                f.flush()
                os.fsync(f.fileno())
            os.replace(tmp, self._path)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise
