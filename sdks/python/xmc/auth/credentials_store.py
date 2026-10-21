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

"""Owner-only on-disk cache of CLI tokens, keyed by deployment origin."""

from __future__ import annotations

import dataclasses
import json
import os
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any, TextIO

CACHE_VERSION = 1
EXPIRY_MARGIN_SECONDS = 60


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
    """Reads the owner-only credentials cache."""

    def __init__(self, path: Path, warnings: TextIO = sys.stderr) -> None:
        """Initializes the store at `path`, reporting unreadable files to `warnings`."""
        self._path = path
        self._warnings = warnings

    def get(self, origin: str) -> StoredCredential | None:
        """Returns the possibly expired credential issued by exactly this origin."""
        return _matching(origin, self._read().get(origin))

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
