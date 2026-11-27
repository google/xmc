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
"""Unit tests for `xm_cloud.cli.credentials`."""

from __future__ import annotations

import contextlib
import json
import os
import pathlib
import stat
import tempfile
import unittest
from collections.abc import Iterator
from unittest import mock

from xdash import auth as xdash_auth

from xm_cloud.cli import credentials

_SERVER = "https://xmc.example.com"
_ENTRY = {"access_token": "t", "email": "u@example.com", "expires_at": 999}


class CredentialsTest(unittest.TestCase):
    """Unit tests for `xm_cloud.cli.credentials`."""

    def setUp(self) -> None:
        super().setUp()
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self._xdg = temp_dir.name
        self._path = pathlib.Path(self._xdg) / "credentials.json"

    def _write_private(self, content: str) -> pathlib.Path:
        self._path.write_text(content, encoding="utf-8")
        os.chmod(self._path, 0o600)
        return self._path

    @contextlib.contextmanager
    def _pin_home(self) -> Iterator[None]:
        # Path.home() reads USERPROFILE on Windows, so patch it as well as HOME.
        home = {"HOME": self._xdg, "USERPROFILE": self._xdg}
        with (
            mock.patch.dict(os.environ, home, clear=True),
            mock.patch.object(pathlib.Path, "home", return_value=pathlib.Path(self._xdg)),
        ):
            yield

    def test_resolve_server_precedence(self) -> None:
        cases = [
            ({"XMC_API_URL": "https://a.io/v1", "XMANAGER_UI_URL": "https://b.io"}, "https://a.io"),
            ({"XMANAGER_UI_URL": "https://x.dev/ui"}, "https://x.dev"),
            ({"XMC_DOMAIN": "xmc.example.com"}, "https://xmc.example.com"),
            (
                {"XMANAGER_ENDPOINT": "dns:///grpc.a.dev:443", "XMC_API_URL": "https://b.dev"},
                "https://a.dev",
            ),
            (
                {"XMANAGER_ENDPOINT": "localhost:50051", "XMC_API_URL": "https://b.dev/v1"},
                "https://b.dev",
            ),
            ({"XMC_API_URL": "http://127.0.0.1:8080"}, "http://127.0.0.1:8080"),
        ]
        for env, expected in cases:
            with self.subTest(env=env):
                self.assertEqual(credentials.resolve_server(env), expected)

    def test_resolve_server_without_config_raises_no_server_message(self) -> None:
        with self.assertRaises(credentials.AuthError) as ctx:
            credentials.resolve_server({})

        self.assertEqual(str(ctx.exception), credentials.NO_SERVER_MESSAGE)

    def test_resolve_server_drops_grpc_prefixes_and_default_ports(self) -> None:
        cases = [
            (
                {"XMANAGER_ENDPOINT": "dns:///grpc.api.prod.example.com:443"},
                "https://prod.example.com",
            ),
            ({"XMANAGER_ENDPOINT": "https://grpc.x.dev:8443"}, "https://x.dev:8443"),
            ({"XMANAGER_ENDPOINT": "http://localhost:8080"}, "http://localhost:8080"),
            ({"XMC_API_URL": "https://XMC.example.com:443/v1"}, "https://xmc.example.com"),
            ({"XMC_DOMAIN": "grpc.x.dev"}, "https://x.dev"),
        ]
        for env, expected in cases:
            with self.subTest(env=env):
                self.assertEqual(credentials.resolve_server(env), expected)

    def test_resolve_server_rejects_invalid_values(self) -> None:
        for bad_env in (
            {"XMC_API_URL": "http://xmc.example.com"},
            {"XMC_API_URL": "http://localhost:0"},
            {"XMC_API_URL": "https://xmc.example.com?q=1"},
            {"XMC_API_URL": "https://xmc.example.com#frag"},
            {"XMC_API_URL": "https://user:secret@xmc.example.com"},
            {"XMC_API_URL": "https://user@xmc.example.com"},
            {"XMC_API_URL": "https://@xmc.example.com"},
            {"XMC_API_URL": "https://[::1"},
            {"XMC_API_URL": "https://xmc.example.com:abc"},
            {"XMC_API_URL": "https://xmc.example.\ncom"},
            {"XMC_API_URL": "https://xmc.example .com"},
            {"XMC_API_URL": "https://xmc.example.com\x07"},
            {"XMC_API_URL": "xmc.example.com"},
            {"XMC_API_URL": "https://0.0.0.0"},
            {"XMANAGER_UI_URL": "xmc.example.com:8443"},
            {"XMC_DOMAIN": "xmc.example.com/ui"},
            {"XMC_DOMAIN": "user@xmc.example.com"},
            {"XMANAGER_ENDPOINT": "api.example.com:443", "XMC_API_URL": _SERVER},
            {"XMANAGER_ENDPOINT": "dns:///grpc."},
            {"XMANAGER_ENDPOINT": "grpc.localhost:443"},
            {"XMANAGER_ENDPOINT": "grpc.xmc.example.com:abc"},
            {"XMANAGER_ENDPOINT": "grpc.xmc.example.com:0"},
            {"XMANAGER_ENDPOINT": "grpc.xmc.example.com:443?q=1"},
            {"XMANAGER_ENDPOINT": "grpc.xmc. example.com:443"},
            {"XMANAGER_ENDPOINT": "u@grpc.xmc.example.com"},
        ):
            with self.subTest(env=bad_env), self.assertRaises(credentials.AuthError):
                credentials.resolve_server(bad_env)

    def test_resolve_server_rejects_malformed_grpc_hosts(self) -> None:
        # Only an IPv6 address may carry several colons, and only a loopback one is
        # accepted; `dns://<authority>` needs a `/` before the target.
        for endpoint in (
            "grpc.xmc.example.com:1:2",
            "2001:db8::1",
            "::1:50051",
            "dns://8.8.8.8",
            "dns:///",
            "grpc://",
            ":443",
        ):
            env = {"XMANAGER_ENDPOINT": endpoint, "XMC_API_URL": _SERVER}
            with (
                self.subTest(endpoint=endpoint),
                self.assertRaisesRegex(credentials.AuthError, "XMANAGER_ENDPOINT"),
            ):
                credentials.resolve_server(env)

    def test_resolve_server_rejects_non_printable_characters_without_echoing_them(self) -> None:
        # Surrogates stand for undecodable bytes in os.environ; C1 controls and
        # bidi or zero-width format characters could spoof the printed origin.
        for char in ("\udcff", "\x9b", "\x85", "\u202e", "\u200b", "\x1b"):
            for env in (
                {"XMC_API_URL": f"https://xmc{char}.example.com"},
                {"XMC_DOMAIN": f"xmc{char}.example.com"},
                {"XMANAGER_ENDPOINT": f"grpc.xmc{char}.example.com:443"},
            ):
                with (
                    self.subTest(char=ascii(char), env=list(env)),
                    self.assertRaisesRegex(credentials.AuthError, "non-printable") as ctx,
                ):
                    credentials.resolve_server(env)
                self.assertNotIn(char, str(ctx.exception))

    def test_resolve_server_rejects_grpc_targets_with_a_path(self) -> None:
        # xdash.auth reads the last path segment as the host, so these would yield a
        # cache key that xdash never looks up (or silently fall through to XMC_API_URL).
        for endpoint in (
            "grpc.xmc.example.com:443/extra",
            "grpc.xmc.example.com:443/grpc.other.example.com",
            "grpc.xmc.example.com:443/",
            "localhost:50051/extra",
            "dns:///grpc.xmc.example.com:443/",
            "dns:///grpc.xmc.example.com:443/localhost",
            "dns:///grpc.xmc.example.com:443/grpc.other.example.com",
            "grpc://grpc.xmc.example.com:443/localhost:50051",
            "grpc://grpc.xmc.example.com:443/grpc.other.example.com",
            "dns://8.8.8.8/grpc.xmc.example.com:443/localhost",
            "dns://8.8.8.8/grpc.xmc.example.com:443/grpc.other.example.com",
        ):
            env = {"XMANAGER_ENDPOINT": endpoint, "XMC_API_URL": _SERVER}
            with (
                self.subTest(endpoint=endpoint),
                self.assertRaisesRegex(credentials.AuthError, "XMANAGER_ENDPOINT"),
            ):
                credentials.resolve_server(env)

    def test_resolve_server_raises_where_xdash_falls_through(self) -> None:
        # xdash.auth skips an unsupported XMANAGER_ENDPOINT and reads XMANAGER_UI_URL;
        # the CLI refuses instead, so it never acts on a deployment the user did not name.
        for endpoint in ("grpc-xmc.example.com:443", "grpc.xmc.example.com:443/extra"):
            env = {"XMANAGER_ENDPOINT": endpoint, "XMANAGER_UI_URL": _SERVER}
            with (
                self.subTest(endpoint=endpoint),
                self.assertRaisesRegex(credentials.AuthError, "Unset it or run `xmc logout --all`"),
            ):
                credentials.resolve_server(env)

    def test_resolve_server_errors_do_not_echo_credentials(self) -> None:
        for env in (
            {"XMC_API_URL": "https://user:s3cret@xmc.example.com"},
            {"XMC_API_URL": "https://user:s3cret@xmc.example.com:abc"},
            {"XMC_API_URL": "https://xmc.example.com/?token=s3cret"},
            {"XMANAGER_ENDPOINT": "grpc://user:s3cret@grpc.xmc.example.com:443"},
        ):
            with self.subTest(env=env), self.assertRaises(credentials.AuthError) as ctx:
                credentials.resolve_server(env)
            self.assertNotIn("s3cret", str(ctx.exception))

    def test_resolve_server_errors_do_not_echo_a_url_path(self) -> None:
        # A path can carry a token; errors echo at most the normalized origin.
        for env in (
            {"XMC_API_URL": "http://evil.example/oauth/SECRET_TOKEN"},
            {"XMC_API_URL": "https://xmc.example.com:99999/SECRET_TOKEN"},
            {"XMC_API_URL": "https://xmc.example.com:0/SECRET_TOKEN"},
            {"XMC_API_URL": "ftp://xmc.example.com/SECRET_TOKEN"},
            {"XMANAGER_UI_URL": "xmc.example.com/SECRET_TOKEN"},
            {"XMC_DOMAIN": "xmc.example.com/SECRET_TOKEN"},
            {"XMANAGER_ENDPOINT": "grpc.xmc.example.com:443/SECRET_TOKEN"},
            {"XMANAGER_ENDPOINT": "dns:///grpc.xmc.example.com:443/SECRET_TOKEN"},
            {"XMANAGER_ENDPOINT": "http://evil.example/SECRET_TOKEN"},
        ):
            with self.subTest(env=env), self.assertRaises(credentials.AuthError) as ctx:
                credentials.resolve_server(env)
            self.assertNotIn("SECRET_TOKEN", str(ctx.exception))
            self.assertIn(next(iter(env)), str(ctx.exception))

    def test_resolve_server_errors_name_the_problem(self) -> None:
        cases = [
            ({"XMC_API_URL": "http://evil.example/x"}, "http://evil.example must use https://"),
            ({"XMC_API_URL": "https://xmc.example.com:0"}, "port in 1-65535"),
            ({"XMC_DOMAIN": "xmc.example.com/ui"}, "a domain must not have a path"),
            ({"XMANAGER_ENDPOINT": "grpc.xmc.example.com:0"}, "port outside 1-65535"),
            ({"XMANAGER_ENDPOINT": "grpc.xmc.example.com:99999"}, "port outside 1-65535"),
            ({"XMANAGER_ENDPOINT": "grpc.xmc.example.com:443/x"}, "must not have a path"),
            ({"XMANAGER_ENDPOINT": "xmc.example.com:443"}, "got host xmc.example.com\\)"),
            ({"XMANAGER_ENDPOINT": "grpc.localhost"}, "grpc.localhost does not name"),
        ]
        for env, message in cases:
            with (
                self.subTest(env=env),
                self.assertRaisesRegex(credentials.AuthError, message),
            ):
                credentials.resolve_server(env)

    def test_resolve_server_matches_the_xdash_cache_key(self) -> None:
        # xdash.auth looks up cached tokens by this origin, so the writer must agree.
        envs = [
            {"XMANAGER_ENDPOINT": "dns:///grpc.api.prod.example.com:443"},
            {"XMANAGER_ENDPOINT": "grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "https://grpc.x.dev"},
            {"XMANAGER_ENDPOINT": "https://grpc.x.dev:8443"},
            {"XMANAGER_ENDPOINT": "https://xmc.example.com"},
            {"XMANAGER_ENDPOINT": "http://localhost:8080"},
            {"XMANAGER_ENDPOINT": "localhost:50051", "XMC_API_URL": "https://b.dev/v1"},
            {"XMANAGER_ENDPOINT": "grpc://grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "dns://8.8.8.8/grpc.x.dev:443"},
            {"XMANAGER_ENDPOINT": "grpc://localhost:50051", "XMC_API_URL": "https://b.dev"},
            {"XMANAGER_ENDPOINT": "::1", "XMC_API_URL": "https://b.dev"},
            {"XMANAGER_ENDPOINT": "dns:///::1", "XMC_API_URL": "https://b.dev"},
            {"XMANAGER_ENDPOINT": "grpc://::1", "XMC_API_URL": "https://b.dev"},
            {"XMANAGER_ENDPOINT": "dns://8.8.8.8/::1", "XMC_API_URL": "https://b.dev"},
            {"XMANAGER_ENDPOINT": "[::1]:50051", "XMC_API_URL": "https://b.dev"},
            {"XMC_API_URL": "https://XMC.example.com:443/v1"},
            {"XMC_API_URL": "https://xmc.example.com:8443"},
            {"XMC_API_URL": "https://grpc.api.x.dev"},
            {"XMC_API_URL": "http://[::1]:80"},
            {"XMANAGER_UI_URL": "https://grpc.x.dev/ui"},
            {"XMC_DOMAIN": "grpc.x.dev"},
            {"XMC_DOMAIN": "xmc.example.com:8443"},
        ]
        for env in envs:
            with self.subTest(env=env), mock.patch.dict(os.environ, env, clear=True):
                self.assertEqual(credentials.resolve_server(env), xdash_auth.resolve_xmc_origin())

    def test_credentials_path_matches_the_xdash_credentials_file(self) -> None:
        envs = [
            {},
            {"XMC_CREDENTIALS_FILE": os.path.join(self._xdg, "custom.json")},
            {"XMC_CREDENTIALS_FILE": "~/custom.json"},
            {"XMC_CREDENTIALS_FILE": "relative.json", "XDG_CONFIG_HOME": self._xdg},
            {"XDG_CONFIG_HOME": "~/cfg"},
            {"XDG_CONFIG_HOME": "relative"},
        ]
        for env in envs:
            patched_env = {"HOME": self._xdg, "USERPROFILE": self._xdg, **env}
            with self.subTest(env=env), mock.patch.dict(os.environ, patched_env, clear=True):
                self.assertEqual(
                    credentials.credentials_path(env), xdash_auth.get_credentials_file_path()
                )

    def test_credentials_path_expands_user_in_xdg_config_home(self) -> None:
        with self._pin_home():
            path = credentials.credentials_path({"XDG_CONFIG_HOME": "~/cfg"})

        self.assertEqual(path, pathlib.Path(self._xdg) / "cfg" / "xmc" / "credentials.json")

    def test_credentials_path_falls_back_for_relative_overrides(self) -> None:
        expected = pathlib.Path(self._xdg) / ".config" / "xmc" / "credentials.json"
        for env in ({"XDG_CONFIG_HOME": "relative"}, {"XMC_CREDENTIALS_FILE": "relative"}):
            with self.subTest(env=env), self._pin_home():
                self.assertEqual(credentials.credentials_path(env), expected)

    def test_credentials_path_returns_an_absolute_override(self) -> None:
        custom_file = os.path.join(self._xdg, "custom_creds.json")

        path = credentials.credentials_path({"XMC_CREDENTIALS_FILE": custom_file})

        self.assertEqual(path, pathlib.Path(custom_file))

    def test_credentials_path_reports_a_missing_home_directory(self) -> None:
        with (
            mock.patch.object(pathlib.Path, "home", side_effect=RuntimeError("no home")),
            self.assertRaisesRegex(credentials.AuthError, "set XMC_CREDENTIALS_FILE"),
        ):
            credentials.credentials_path({})

    def test_credentials_path_rejects_a_relative_home(self) -> None:
        # A relative $HOME would read and write the token cache in the working directory.
        relative_home = {"HOME": "relative/dir", "USERPROFILE": "relative/dir"}
        for env in ({}, {"XMC_CREDENTIALS_FILE": "~/creds.json"}, {"XDG_CONFIG_HOME": "rel"}):
            with (
                self.subTest(env=env),
                mock.patch.dict(os.environ, relative_home, clear=True),
                self.assertRaisesRegex(credentials.AuthError, r"relative path; set XMC_CRED"),
            ):
                credentials.credentials_path(env)

    def test_credentials_path_rejects_nul_characters(self) -> None:
        for env in (
            {"XMC_CREDENTIALS_FILE": os.path.join(self._xdg, "creds\x00.json")},
            {"XDG_CONFIG_HOME": os.path.join(self._xdg, "x\x00dg")},
        ):
            with (
                self.subTest(env=env),
                self.assertRaisesRegex(credentials.AuthError, "must not contain NUL"),
            ):
                credentials.credentials_path(env)

    def test_load_credentials_reports_a_nul_path_as_auth_error(self) -> None:
        path = pathlib.Path(self._xdg) / "creds\x00.json"

        with self.assertRaisesRegex(credentials.AuthError, "Refusing to read"):
            credentials.load_credentials(path)

    def test_save_credentials_reports_a_nul_path_as_auth_error(self) -> None:
        path = pathlib.Path(self._xdg) / "creds\x00.json"

        with self.assertRaisesRegex(credentials.AuthError, "Cannot write credentials file"):
            credentials.save_credentials(path, {})

    def test_save_credentials_reports_deep_nesting_the_encoder_cannot_handle(self) -> None:
        # Python 3.11/3.12 serialize `indent=2` with the recursive pure-Python
        # encoder, so an entry that json.loads accepted can exceed the recursion limit.
        nested: list[object] = []
        for _ in range(5000):
            nested = [nested]
        with (
            mock.patch.object(json.encoder, "c_make_encoder", None),
            self.assertRaisesRegex(credentials.AuthError, "Cannot write credentials file"),
        ):
            credentials.save_credentials(self._path, {_SERVER: {"x": nested}})

        self.assertFalse(self._path.exists())

    def test_load_credentials_returns_empty_for_a_missing_file(self) -> None:
        self.assertEqual(credentials.load_credentials(self._path), {})

    def test_save_then_load_roundtrips_sorted_entries(self) -> None:
        other = "https://z.example.com"

        credentials.save_credentials(self._path, {other: _ENTRY, _SERVER: _ENTRY})

        raw = self._path.read_text(encoding="utf-8")
        self.assertLess(raw.index(_SERVER), raw.index(other))
        self.assertEqual(credentials.load_credentials(self._path), {other: _ENTRY, _SERVER: _ENTRY})

    def _set_umask(self, mask: int) -> None:
        self.addCleanup(os.umask, os.umask(mask))

    def test_save_credentials_creates_0600_file_in_0700_dir(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        self._set_umask(0o022)
        path = pathlib.Path(self._xdg) / "xmc" / "credentials.json"

        credentials.save_credentials(path, {})

        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)

    def test_save_credentials_restricts_every_new_ancestor_regardless_of_umask(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        # 0o277 strips the owner's write and execute bits from mkdir's mode, so
        # 0700 holds only if each created directory is chmod-ed explicitly.
        self._set_umask(0o277)
        base = pathlib.Path(self._xdg)
        path = base / "new1" / "new2" / "credentials.json"

        credentials.save_credentials(path, {})

        for directory in (base / "new1", base / "new1" / "new2"):
            with self.subTest(directory=directory.name):
                self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(path.stat().st_mode) & 0o077, 0)
        self.assertEqual(credentials.load_credentials(path), {})

    def test_save_credentials_keeps_the_mode_of_an_existing_directory(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        for mode in (0o755, 0o775, 0o1777):
            with self.subTest(mode=oct(mode)):
                temp_dir = tempfile.TemporaryDirectory()
                self.addCleanup(temp_dir.cleanup)
                existing = pathlib.Path(temp_dir.name)
                os.chmod(existing, mode)
                path = existing / "credentials.json"

                credentials.save_credentials(path, {})

                self.assertEqual(stat.S_IMODE(existing.stat().st_mode), mode)
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
                self.assertEqual(credentials.load_credentials(path), {})

    def test_save_credentials_tolerates_concurrent_directory_creation(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        concurrent = pathlib.Path(temp_dir.name) / "concurrent"
        path = concurrent / "credentials.json"
        real_mkdir = pathlib.Path.mkdir

        def race_mkdir(
            target: pathlib.Path, mode: int = 0o777, parents: bool = False, exist_ok: bool = False
        ) -> None:
            if target == concurrent:
                real_mkdir(target, mode=0o755)
                raise FileExistsError(target)
            real_mkdir(target, mode=mode, parents=parents, exist_ok=exist_ok)

        with mock.patch.object(pathlib.Path, "mkdir", race_mkdir):
            credentials.save_credentials(path, {})

        self.assertEqual(stat.S_IMODE(concurrent.stat().st_mode), 0o755)
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(credentials.load_credentials(path), {})

    def test_load_credentials_accepts_owner_only_modes(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        path = self._write_private('{"version": 1, "deployments": {}}')
        for mode in (0o400, 0o600, 0o700):
            with self.subTest(mode=oct(mode)):
                os.chmod(path, mode)
                self.assertEqual(credentials.load_credentials(path), {})

    def test_load_credentials_rejects_group_or_world_bits(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX permissions only")
        path = self._write_private('{"version": 1, "deployments": {}}')
        for mode in (0o640, 0o604, 0o660, 0o644):
            with self.subTest(mode=oct(mode)):
                os.chmod(path, mode)
                with self.assertRaisesRegex(
                    credentials.AuthError, "must not be accessible by group or others"
                ):
                    credentials.load_credentials(path)

    def test_load_credentials_rejects_corrupt_or_unsupported_content(self) -> None:
        cases = [
            ("{not json", "Corrupt credentials file"),
            ('{"version": 2, "deployments": {}}', "Unsupported credentials schema"),
            ('{"version": true, "deployments": {}}', "Unsupported credentials schema"),
            ("[]", "Unsupported credentials schema"),
            ("null", "Unsupported credentials schema"),
        ]
        for content, message in cases:
            with self.subTest(content=content):
                path = self._write_private(content)
                with self.assertRaisesRegex(credentials.AuthError, f"{message}.*xmc logout --all"):
                    credentials.load_credentials(path)

    def test_load_credentials_reports_deeply_nested_json_as_corrupt(self) -> None:
        # json raises RecursionError, not ValueError, past the interpreter's nesting limit.
        path = self._write_private("[" * 100_000)

        with self.assertRaisesRegex(credentials.AuthError, "Corrupt credentials file"):
            credentials.load_credentials(path)

    def test_load_credentials_treats_non_dict_deployments_as_empty(self) -> None:
        path = self._write_private('{"version": 1, "deployments": []}')

        self.assertEqual(credentials.load_credentials(path), {})

    def test_load_credentials_drops_entries_that_are_not_objects(self) -> None:
        path = self._write_private(
            '{"version": 1, "deployments": {"https://a.io": {"access_token": "t"},'
            ' "https://b.io": null, "https://c.io": "s"}}'
        )

        self.assertEqual(
            credentials.load_credentials(path), {"https://a.io": {"access_token": "t"}}
        )

    def test_load_credentials_rejects_a_directory(self) -> None:
        with self.assertRaisesRegex(credentials.AuthError, "must be a regular file"):
            credentials.load_credentials(pathlib.Path(self._xdg))

    def test_load_credentials_rejects_a_symlink(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX symlinks only")
        target = self._write_private('{"version": 1, "deployments": {}}')
        link = pathlib.Path(self._xdg) / "link.json"
        link.symlink_to(target)

        with self.assertRaisesRegex(credentials.AuthError, "Refusing to read"):
            credentials.load_credentials(link)

    def test_load_credentials_rejects_a_fifo_without_blocking(self) -> None:
        if not hasattr(os, "mkfifo"):
            self.skipTest("POSIX FIFOs only")
        os.mkfifo(self._path, 0o600)

        with self.assertRaisesRegex(credentials.AuthError, "must be a regular file"):
            credentials.load_credentials(self._path)

    def test_load_credentials_closes_fd_when_fstat_or_fdopen_fails(self) -> None:
        credentials.save_credentials(self._path, {})
        for target_name in ("fdopen", "fstat"):
            with (
                self.subTest(failing_call=target_name),
                mock.patch.object(os, target_name, side_effect=OSError(f"{target_name} failed")),
                mock.patch.object(os, "close", wraps=os.close) as close_mock,
                self.assertRaisesRegex(credentials.AuthError, "Refusing to read"),
            ):
                credentials.load_credentials(self._path)
            close_mock.assert_called_once()

    def test_load_credentials_rejects_a_foreign_owner(self) -> None:
        if not hasattr(os, "getuid"):
            self.skipTest("POSIX ownership only")
        credentials.save_credentials(self._path, {})

        with (
            mock.patch.object(os, "getuid", return_value=os.getuid() + 1),
            self.assertRaisesRegex(credentials.AuthError, "must be owned by the current user"),
        ):
            credentials.load_credentials(self._path)

    def test_write_credentials_closes_fd_and_unlinks_tmp_on_fdopen_failure(self) -> None:
        path = pathlib.Path(self._xdg) / "xmc" / "credentials.json"
        with (
            mock.patch.object(os, "fdopen", side_effect=OSError("fdopen failed")),
            mock.patch.object(os, "close", wraps=os.close) as close_mock,
            self.assertRaisesRegex(credentials.AuthError, "Cannot write credentials file"),
        ):
            credentials.save_credentials(path, {})

        close_mock.assert_called_once()
        self.assertEqual(list(path.parent.glob(".credentials.*")), [])

    def test_write_credentials_does_not_double_close_after_fdopen_owns_the_fd(self) -> None:
        # Once fdopen owns the descriptor, closing it again could close an unrelated
        # file that reused the number.
        path = pathlib.Path(self._xdg) / "xmc" / "credentials.json"
        with (
            mock.patch.object(os, "replace", side_effect=OSError("replace failed")),
            mock.patch.object(os, "close", wraps=os.close) as close_mock,
            self.assertRaisesRegex(credentials.AuthError, "replace failed"),
        ):
            credentials.save_credentials(path, {})

        close_mock.assert_not_called()
        self.assertEqual(list(path.parent.glob(".credentials.*")), [])

    def test_load_credentials_does_not_double_close_after_fdopen_owns_the_fd(self) -> None:
        credentials.save_credentials(self._path, {})
        with (
            mock.patch.object(credentials, "_read_bounded", side_effect=OSError("read failed")),
            mock.patch.object(os, "close", wraps=os.close) as close_mock,
            self.assertRaisesRegex(credentials.AuthError, "read failed"),
        ):
            credentials.load_credentials(self._path)

        close_mock.assert_not_called()

    def test_load_credentials_enforces_the_size_cap(self) -> None:
        document = '{"version": 1, "deployments": {}}'
        at_cap = document.ljust(credentials._MAX_CREDENTIALS_BYTES)

        self.assertEqual(credentials.load_credentials(self._write_private(at_cap)), {})
        self._write_private(at_cap + " ")
        with self.assertRaisesRegex(credentials.AuthError, "larger than 1048576 bytes"):
            credentials.load_credentials(self._path)

    def test_credentials_file_exists_is_false_for_a_missing_path(self) -> None:
        self.assertFalse(credentials.credentials_file_exists(self._path))

    def test_credentials_file_exists_is_true_for_a_regular_file(self) -> None:
        self._write_private("{}")

        self.assertTrue(credentials.credentials_file_exists(self._path))

    def test_credentials_file_exists_is_true_for_dangling_and_looping_symlinks(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX symlinks only")
        base = pathlib.Path(self._xdg)
        dangling, loop = base / "dangling.json", base / "loop.json"
        dangling.symlink_to(base / "missing.json")
        loop.symlink_to(loop)
        for link in (dangling, loop):
            with self.subTest(link=link.name):
                self.assertTrue(credentials.credentials_file_exists(link))

    def test_credentials_file_exists_does_not_follow_a_symlink(self) -> None:
        if os.name != "posix":
            self.skipTest("POSIX symlinks only")
        self._path.symlink_to(pathlib.Path(self._xdg) / "unreachable.json")

        # exists() follows the link; EACCES on its target must not matter.
        with mock.patch.object(pathlib.Path, "exists", side_effect=PermissionError("denied")):
            self.assertTrue(credentials.credentials_file_exists(self._path))

    def test_credentials_file_exists_wraps_os_errors(self) -> None:
        with (
            mock.patch.object(pathlib.Path, "is_symlink", side_effect=PermissionError("denied")),
            self.assertRaisesRegex(credentials.AuthError, "Cannot access credentials file"),
        ):
            credentials.credentials_file_exists(self._path)


if __name__ == "__main__":
    unittest.main()
