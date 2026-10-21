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

"""Unit tests for xmc.auth.credentials_store."""

from __future__ import annotations

import dataclasses
import io
import json
import os
import stat
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from xmc.auth import AuthError, credentials_store
from xmc.auth.credentials_store import CredentialsStore, StoredCredential

_ORIGIN = "https://xmc.example.com"
_CRED = StoredCredential(
    server=_ORIGIN, access_token="tok-123", email="user@example.com", expires_at=2_000_000_000
)
_OTHER = dataclasses.replace(_CRED, server="https://b.example.com")


class StoredCredentialTest(unittest.TestCase):
    """Tests expiry evaluation."""

    def test_is_expired_honors_margin(self) -> None:
        """Tokens within the safety margin of expiry count as expired."""
        margin = credentials_store.EXPIRY_MARGIN_SECONDS
        self.assertFalse(_CRED.is_expired(_CRED.expires_at - margin - 1))
        self.assertTrue(_CRED.is_expired(_CRED.expires_at - margin))
        self.assertTrue(_CRED.is_expired(_CRED.expires_at + 1))


class DefaultCredentialsPathTest(unittest.TestCase):
    """Tests cache path resolution."""

    def test_respects_absolute_xdg_config_home(self) -> None:
        """An absolute XDG_CONFIG_HOME is used; relative or empty values are ignored."""
        home = Path("/home/u")
        cases = {
            "/xdg": Path("/xdg/xmc/credentials.json"),
            "relative/dir": home / ".config/xmc/credentials.json",
            "": home / ".config/xmc/credentials.json",
        }
        for xdg, want in cases.items():
            with self.subTest(xdg=xdg), mock.patch.object(Path, "home", return_value=home):
                got = credentials_store.default_credentials_path({"XDG_CONFIG_HOME": xdg})
                self.assertEqual(got, want)


class CredentialsStoreTest(unittest.TestCase):
    """Tests cache reads, writes, permissions, ownership, and corruption handling."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.path = Path(tmp.name) / "config" / "xmc" / "credentials.json"
        self.warnings = io.StringIO()
        self.store = CredentialsStore(self.path, warnings=self.warnings)

    def _write_raw(self, data: object) -> None:
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.path.write_text(data if isinstance(data, str) else json.dumps(data))
        self.path.chmod(0o600)

    def test_round_trip_and_schema(self) -> None:
        """put/get round-trips and writes the documented v1 schema keyed by origin."""
        self.store.put(_CRED)
        self.assertEqual(self.store.get(_ORIGIN), _CRED)
        data = json.loads(self.path.read_text())
        self.assertEqual(data, {"version": 1, "deployments": {_ORIGIN: dataclasses.asdict(_CRED)}})

    def test_get_requires_exact_origin(self) -> None:
        """Lookups match scheme, host, and port exactly, and entries must match their key."""
        self.store.put(_CRED)
        for origin in ("http://xmc.example.com", "https://xmc.example.com:8443"):
            self.assertIsNone(self.store.get(origin))
        self._write_raw({"version": 1, "deployments": {_ORIGIN: dataclasses.asdict(_OTHER)}})
        self.assertIsNone(self.store.get(_ORIGIN))

    def test_permissions(self) -> None:
        """The directory is 0700 and the file 0600, even if the dir pre-existed."""
        self.path.parent.mkdir(parents=True, mode=0o755)
        self.store.put(_CRED)
        self.assertEqual(stat.S_IMODE(self.path.parent.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(self.path.stat().st_mode), 0o600)

    def test_put_preserves_other_deployments(self) -> None:
        """Writing one deployment keeps entries for others."""
        self.store.put(_OTHER)
        self.store.put(_CRED)
        self.assertEqual(self.store.get(_OTHER.server), _OTHER)

    def test_write_failure_is_actionable_and_atomic(self) -> None:
        """A failed write raises AuthError, keeps the old file, and removes the temp file."""
        self.store.put(_CRED)
        with mock.patch.object(os, "replace", side_effect=OSError(28, "No space left")):
            with self.assertRaisesRegex(AuthError, "No space left.*run `xmc login` again"):
                self.store.put(dataclasses.replace(_CRED, email="other@example.com"))
        self.assertEqual(self.store.get(_ORIGIN), _CRED)
        self.assertEqual(os.listdir(self.path.parent), ["credentials.json"])

    def test_read_oserror_warns_and_returns_none(self) -> None:
        """An unreadable cache warns and reads as empty."""
        self.path.mkdir(parents=True)
        self.assertIsNone(self.store.get(_ORIGIN))
        self.assertIn("ignoring unreadable", self.warnings.getvalue())

    def test_invalid_contents_are_treated_as_missing(self) -> None:
        """Corrupt, wrong-version, or malformed entries read as missing."""
        entry = dataclasses.asdict(_CRED)
        cases = {
            "corrupt": ("{not json", True),
            "wrong version": ({"version": 2, "deployments": {_ORIGIN: entry}}, False),
            "not a dict": ([], False),
            "bad deployments": ({"version": 1, "deployments": []}, False),
            "bad entry": ({"version": 1, "deployments": {_ORIGIN: "x"}}, False),
            "empty field": (
                {"version": 1, "deployments": {_ORIGIN: {**entry, "email": ""}}},
                False,
            ),
            "bool expiry": (
                {"version": 1, "deployments": {_ORIGIN: {**entry, "expires_at": True}}},
                False,
            ),
        }
        for name, (content, warns) in cases.items():
            with self.subTest(name):
                self.warnings.seek(0)
                self.warnings.truncate()
                self._write_raw(content)
                self.assertIsNone(self.store.get(_ORIGIN))
                self.assertEqual("not valid JSON" in self.warnings.getvalue(), warns)

    def test_missing_file(self) -> None:
        """A missing cache reads as empty without warnings."""
        self.assertIsNone(self.store.get(_ORIGIN))
        self.assertEqual(self.warnings.getvalue(), "")

    def test_remove(self) -> None:
        """remove deletes only the given entry and tolerates missing ones."""
        self.store.put(_CRED)
        self.store.put(_OTHER)
        self.store.remove(_ORIGIN)
        self.store.remove(_ORIGIN)
        self.assertIsNone(self.store.get(_ORIGIN))
        self.assertEqual(self.store.get(_OTHER.server), _OTHER)

    def test_servers(self) -> None:
        """servers lists the origins of well-formed, self-consistent entries only."""
        self.assertEqual(self.store.servers(), [])
        entry = dataclasses.asdict(_CRED)
        self._write_raw(
            {"version": 1, "deployments": {_ORIGIN: entry, "https://x.example.com": entry}}
        )
        self.assertEqual(self.store.servers(), [_ORIGIN])


if __name__ == "__main__":
    unittest.main()
