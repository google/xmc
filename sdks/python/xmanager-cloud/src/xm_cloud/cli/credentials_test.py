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

import os
import pathlib
import stat
import tempfile
import unittest
from unittest import mock

from xdash import auth as xdash_auth

from xm_cloud.cli import credentials

_SERVER = "https://xmc.example.com"


class CredentialsTest(unittest.TestCase):
    """Unit tests for `xm_cloud.cli.credentials`."""

    def setUp(self) -> None:
        super().setUp()
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self._xdg = temp_dir.name

    def test_resolve_server_precedence_and_validation(self) -> None:
        cases = [
            (
                {
                    "XMC_API_URL": "https://a.io/v1",
                    "XMANAGER_UI_URL": "https://b.io",
                },
                "https://a.io",
            ),
            ({"XMANAGER_UI_URL": "https://x.dev/ui"}, "https://x.dev"),
            ({"XMC_DOMAIN": "xmc.example.com"}, "https://xmc.example.com"),
            (
                {
                    "XMANAGER_ENDPOINT": "dns:///grpc.a.dev:443",
                    "XMC_API_URL": "https://b.dev",
                },
                "https://a.dev",
            ),
            (
                {
                    "XMANAGER_ENDPOINT": "localhost:50051",
                    "XMC_API_URL": "https://b.dev/v1",
                },
                "https://b.dev",
            ),
            ({"XMC_API_URL": "http://127.0.0.1:8080"}, "http://127.0.0.1:8080"),
        ]
        for env, expected in cases:
            self.assertEqual(credentials.resolve_server(env), expected)
        with self.assertRaises(credentials.AuthError) as ctx:
            credentials.resolve_server({})
        self.assertEqual(str(ctx.exception), credentials.NO_SERVER_MESSAGE)
        for bad_env in (
            {"XMC_API_URL": "http://xmc.example.com"},
            {"XMC_API_URL": "http://localhost:0"},
            {"XMC_API_URL": "https://xmc.example.com?q=1"},
            {"XMC_API_URL": "https://[::1"},
            {"XMC_API_URL": "https://xmc.example.com:abc"},
            {"XMANAGER_ENDPOINT": "api.example.com:443", "XMC_API_URL": _SERVER},
            {"XMANAGER_ENDPOINT": "dns:///grpc."},
        ):
            with self.subTest(env=bad_env), self.assertRaises(credentials.AuthError):
                credentials.resolve_server(bad_env)

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
            patched_env = {"HOME": self._xdg, **env}
            with self.subTest(env=env), mock.patch.dict(os.environ, patched_env, clear=True):
                self.assertEqual(
                    credentials.credentials_path(env), xdash_auth.get_credentials_file_path()
                )

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
        for bad_env in (
            {"XMC_DOMAIN": "xmc.example.com/ui"},
            {"XMC_API_URL": "xmc.example.com"},
            {"XMANAGER_UI_URL": "xmc.example.com:8443"},
            {"XMC_API_URL": "https://0.0.0.0"},
            {"XMANAGER_ENDPOINT": "grpc.localhost:443"},
        ):
            with self.subTest(env=bad_env), self.assertRaises(credentials.AuthError):
                credentials.resolve_server(bad_env)

    def test_credentials_path_expands_user_in_xdg_config_home(self) -> None:
        self.assertEqual(
            credentials.credentials_path({"XDG_CONFIG_HOME": "~/cfg"}),
            pathlib.Path.home() / "cfg" / "xmc" / "credentials.json",
        )

    def test_credentials_path_reports_a_missing_home_directory(self) -> None:
        with (
            mock.patch.object(pathlib.Path, "home", side_effect=RuntimeError("no home")),
            self.assertRaisesRegex(credentials.AuthError, "set XMC_CREDENTIALS_FILE"),
        ):
            credentials.credentials_path({})

    def test_load_credentials_rejects_unreadable_corrupt_or_unknown_files(self) -> None:
        path = pathlib.Path(self._xdg) / "credentials.json"
        cases = [
            ("{not json", "Corrupt credentials file"),
            ('{"version": 2, "deployments": {}}', "Unsupported credentials schema"),
            ("[]", "Unsupported credentials schema"),
        ]
        for content, message in cases:
            with self.subTest(content=content):
                path.write_text(content, encoding="utf-8")
                os.chmod(path, 0o600)
                with self.assertRaisesRegex(credentials.AuthError, message):
                    credentials.load_credentials(path)
        path.write_text('{"version": 1, "deployments": []}', encoding="utf-8")
        self.assertEqual(credentials.load_credentials(path), {})
        with self.assertRaisesRegex(credentials.AuthError, "must be a regular file"):
            credentials.load_credentials(pathlib.Path(self._xdg))
        if os.name == "posix":
            link = pathlib.Path(self._xdg) / "link.json"
            link.symlink_to(path)
            with self.assertRaisesRegex(credentials.AuthError, "Refusing to read"):
                credentials.load_credentials(link)

    def test_load_credentials_closes_fd_and_rejects_foreign_owner(self) -> None:
        path = pathlib.Path(self._xdg) / "xmc" / "credentials.json"
        credentials.save_credentials(path, {})
        for target_name in ("fdopen", "fstat"):
            with (
                self.subTest(failing_call=target_name),
                mock.patch.object(os, target_name, side_effect=OSError(f"{target_name} failed")),
                mock.patch.object(os, "close", wraps=os.close) as close_mock,
                self.assertRaisesRegex(credentials.AuthError, "Refusing to read"),
            ):
                credentials.load_credentials(path)
            close_mock.assert_called_once()

        if hasattr(os, "getuid"):
            with (
                mock.patch.object(os, "getuid", return_value=os.getuid() + 1),
                self.assertRaisesRegex(credentials.AuthError, "must be owned by the current user"),
            ):
                credentials.load_credentials(path)

    def test_credentials_roundtrip_permissions_and_relative_xdg(self) -> None:
        self.assertEqual(
            credentials.credentials_path({"XDG_CONFIG_HOME": "relative"}),
            pathlib.Path.home() / ".config" / "xmc" / "credentials.json",
        )
        self.assertEqual(
            credentials.credentials_path({"XMC_CREDENTIALS_FILE": "relative"}),
            pathlib.Path.home() / ".config" / "xmc" / "credentials.json",
        )
        custom_file = os.path.join(self._xdg, "custom_creds.json")
        self.assertEqual(
            credentials.credentials_path({"XMC_CREDENTIALS_FILE": custom_file}),
            pathlib.Path(custom_file),
        )
        path = credentials.credentials_path({"XDG_CONFIG_HOME": self._xdg})
        self.assertEqual(path, pathlib.Path(self._xdg) / "xmc" / "credentials.json")
        self.assertEqual(credentials.load_credentials(path), {})
        entry = {"access_token": "t", "email": "u@example.com", "expires_at": 999}
        credentials.save_credentials(path, {"https://z.example.com": entry, _SERVER: entry})
        raw = path.read_text(encoding="utf-8")
        self.assertLess(raw.index(_SERVER), raw.index("https://z.example.com"))
        self.assertEqual(
            credentials.load_credentials(path),
            {"https://z.example.com": entry, _SERVER: entry},
        )
        if os.name == "posix":
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)
            os.chmod(path, 0o644)
            with self.assertRaisesRegex(credentials.AuthError, "xmc logout --all"):
                credentials.load_credentials(path)

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


if __name__ == "__main__":
    unittest.main()
