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
"""Unit tests for xdash.auth credential auto-discovery and origin binding."""

from __future__ import annotations

import json
import os
import pathlib
import tempfile
import unittest
from datetime import datetime, timezone
from unittest.mock import MagicMock, patch

from xdash.auth import (
    CLI_TOKEN_EXPIRY_BUFFER_SEC,
    get_credentials_file_path,
    load_cached_cli_token,
    resolve_auth_headers,
    resolve_env_auth_token,
    resolve_xmc_origin,
)


class AuthTest(unittest.TestCase):
    """Tests for XMC token resolution, origin binding, and credentials.json hygiene."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.creds_path = pathlib.Path(self.temp_dir.name) / "credentials.json"

    def _write_credentials(
        self,
        payload: object,
        mode: int = 0o600,
    ) -> pathlib.Path:
        """Writes a credentials.json payload with explicit file permissions."""
        self.creds_path.write_text(json.dumps(payload), encoding="utf-8")
        os.chmod(self.creds_path, mode)
        return self.creds_path

    def _valid_payload(
        self,
        origin: str = "https://xmc.example.com",
        token: str = "cached-cli-jwt",
        expires_offset_sec: float = 3600.0,
    ) -> dict[str, object]:
        """Builds a baseline v1 credentials.json payload."""
        now_utc = datetime.now(timezone.utc).timestamp()
        return {
            "version": 1,
            "deployments": {
                origin: {
                    "access_token": token,
                    "expires_at": now_utc + expires_offset_sec,
                    "email": "user@example.com",
                }
            },
        }

    def test_resolve_env_auth_token_precedence_and_whitespace_rejection(self) -> None:
        with patch.dict(
            "os.environ",
            {"XMC_AUTH_TOKEN": "  primary-tok  ", "AUTH_TOKEN": "fallback-tok"},
            clear=True,
        ):
            self.assertEqual(resolve_env_auth_token(), "primary-tok")

        with patch.dict("os.environ", {"XMC_AUTH_TOKEN": "", "AUTH_TOKEN": "fallback"}, clear=True):
            self.assertEqual(resolve_env_auth_token(), "fallback")

        with patch.dict("os.environ", {}, clear=True):
            self.assertIsNone(resolve_env_auth_token())

        for env_var in ("XMC_AUTH_TOKEN", "AUTH_TOKEN"):
            with (
                patch.dict("os.environ", {env_var: "   \t\n  "}, clear=True),
                self.assertRaisesRegex(ValueError, "XMC_AUTH_TOKEN must not be whitespace-only"),
            ):
                resolve_env_auth_token()

    def test_get_credentials_file_path_precedence(self) -> None:
        with patch.dict(
            "os.environ",
            {
                "XMC_CREDENTIALS_FILE": "/custom/path/creds.json",
                "XDG_CONFIG_HOME": "/xdg/config",
            },
            clear=True,
        ):
            self.assertEqual(get_credentials_file_path(), pathlib.Path("/custom/path/creds.json"))

        # Relative XMC_CREDENTIALS_FILE is ignored; falls through to XDG_CONFIG_HOME
        with patch.dict(
            "os.environ",
            {
                "XMC_CREDENTIALS_FILE": "relative/creds.json",
                "XDG_CONFIG_HOME": "/xdg/config",
            },
            clear=True,
        ):
            self.assertEqual(
                get_credentials_file_path(),
                pathlib.Path("/xdg/config/xmc/credentials.json"),
            )

        with (
            patch.dict("os.environ", {"XDG_CONFIG_HOME": "relative/xdg"}, clear=True),
            patch("pathlib.Path.home", return_value=pathlib.Path("/home/tester")),
        ):
            self.assertEqual(
                get_credentials_file_path(),
                pathlib.Path("/home/tester/.config/xmc/credentials.json"),
            )

    def test_resolve_xmc_origin_from_direct_endpoints(self) -> None:
        with patch.dict("os.environ", {}, clear=True):
            self.assertEqual(
                resolve_xmc_origin("grpc.xmc.example.com:443"),
                "https://xmc.example.com",
            )
            self.assertEqual(
                resolve_xmc_origin("dns:///grpc.api.xmc.example.com:443"),
                "https://xmc.example.com",
            )
            self.assertEqual(
                resolve_xmc_origin("https://xmc.example.com:443/v1/dashboards"),
                "https://xmc.example.com",
            )
            self.assertEqual(
                resolve_xmc_origin("https://grpc.xmc.example.com:8443/v1/dashboards"),
                "https://xmc.example.com:8443",
            )

    def test_resolve_xmc_origin_loopback_falls_back_to_env_chain(self) -> None:
        env_cases = [
            ({"XMANAGER_ENDPOINT": "dns:///grpc.env1.example.com:443"}, "https://env1.example.com"),
            (
                {"XMANAGER_ENDPOINT": "https://env1b.example.com/v1/api"},
                "https://env1b.example.com",
            ),
            ({"XMC_API_URL": "https://env2.example.com:443/api"}, "https://env2.example.com"),
            ({"XMANAGER_UI_URL": "https://env3.example.com/ui/path"}, "https://env3.example.com"),
            ({"XMC_DOMAIN": "env4.example.com"}, "https://env4.example.com"),
            ({"XMANAGER_UI_URL": "http://localhost:8080"}, "http://localhost:8080"),
        ]
        for env, expected in env_cases:
            with self.subTest(env=env), patch.dict("os.environ", env, clear=True):
                self.assertEqual(resolve_xmc_origin("localhost:8080", secure=False), expected)
                self.assertEqual(
                    resolve_xmc_origin("http://127.0.0.1:8080/v1/dashboards"), expected
                )
                self.assertEqual(resolve_xmc_origin("[::1]:50051", secure=False), expected)

        # Explicit HTTP loopback URL falls back to its own origin when no env vars are set
        with patch.dict("os.environ", {}, clear=True):
            self.assertEqual(
                resolve_xmc_origin("http://localhost:8080/v1/dashboards"),
                "http://localhost:8080",
            )
            self.assertEqual(
                resolve_xmc_origin("http://127.0.0.1:80/v1/dashboards"),
                "http://127.0.0.1",
            )
            # XMC_DOMAIN with a non-root path is still rejected
            with patch.dict("os.environ", {"XMC_DOMAIN": "env4.example.com/subpath"}, clear=True):
                self.assertIsNone(resolve_xmc_origin("localhost:8080", secure=False))

    def test_resolve_xmc_origin_rejects_plaintext_and_untrusted_endpoints(self) -> None:
        env = {"XMANAGER_UI_URL": "https://xmc.example.com"}
        with patch.dict("os.environ", env, clear=True):
            # Plaintext HTTP to non-loopback is rejected
            self.assertIsNone(resolve_xmc_origin("http://xmc.example.com/v1/dashboards"))
            self.assertIsNone(resolve_xmc_origin("http://0.0.0.0:8080/v1/dashboards"))
            # Insecure gRPC to non-loopback is rejected
            self.assertIsNone(resolve_xmc_origin("grpc.xmc.example.com:443", secure=False))
            self.assertIsNone(resolve_xmc_origin("0.0.0.0:8080", secure=False))
            # Untrusted third-party gRPC host does not inherit env origin
            self.assertIsNone(resolve_xmc_origin("evil.example.org:443", secure=True))
            # Matching configured origin host succeeds over TLS
            self.assertEqual(
                resolve_xmc_origin("xmc.example.com:443", secure=True),
                "https://xmc.example.com",
            )
            # URL with embedded credentials is rejected
            self.assertIsNone(resolve_xmc_origin("https://user:pass@xmc.example.com/v1"))

    def test_load_cached_cli_token_success(self) -> None:
        self._write_credentials(self._valid_payload("https://xmc.example.com", "my-cli-token"))
        with patch.dict(
            "os.environ",
            {
                "XMC_CREDENTIALS_FILE": str(self.creds_path),
                "XMANAGER_ENDPOINT": "dns:///grpc.xmc.example.com:443",
            },
            clear=True,
        ):
            self.assertEqual(load_cached_cli_token("localhost:8080", secure=False), "my-cli-token")
            self.assertEqual(
                load_cached_cli_token("https://xmc.example.com/v1/dashboards"),
                "my-cli-token",
            )

    def test_load_cached_cli_token_enforces_expiry_with_30s_buffer(self) -> None:
        # Token expiring in 15s (within the 30s clock-skew buffer) is rejected
        self._write_credentials(
            self._valid_payload(
                "https://xmc.example.com",
                "expiring-soon-token",
                expires_offset_sec=CLI_TOKEN_EXPIRY_BUFFER_SEC - 15.0,
            )
        )
        with (
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True),
            self.assertLogs("xdash.auth", level="WARNING") as log_cm,
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))
        self.assertTrue(any("xmanager login" in msg for msg in log_cm.output))

    def test_load_cached_cli_token_rejects_bad_permissions_and_uid(self) -> None:
        self._write_credentials(self._valid_payload(), mode=0o644)
        with (
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True),
            self.assertLogs("xdash.auth", level="WARNING") as log_cm,
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))
        self.assertTrue(any("permissions" in msg for msg in log_cm.output))

        # Restore 0600 permissions but simulate uid mismatch
        os.chmod(self.creds_path, 0o600)
        with (
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True),
            patch("xdash.auth.os.getuid", return_value=os.getuid() + 999),
            self.assertLogs("xdash.auth", level="WARNING") as uid_log,
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))
        self.assertTrue(any("owned by uid" in msg for msg in uid_log.output))

    def test_load_cached_cli_token_rejects_non_regular_and_corrupt_files(self) -> None:
        # Missing file returns None quietly
        with patch.dict(
            "os.environ",
            {"XMC_CREDENTIALS_FILE": str(pathlib.Path(self.temp_dir.name) / "nonexistent.json")},
            clear=True,
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))

        # Directory instead of regular file
        with (
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": self.temp_dir.name}, clear=True),
            self.assertLogs("xdash.auth", level="WARNING") as dir_log,
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))
        self.assertTrue(any("not a regular file" in msg for msg in dir_log.output))

        # Corrupt JSON
        self.creds_path.write_text("{not-json", encoding="utf-8")
        os.chmod(self.creds_path, 0o600)
        with (
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True),
            self.assertLogs("xdash.auth", level="WARNING"),
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))

        # Unsupported version or boolean expires_at
        for bad_payload in (
            {"version": 2, "deployments": {}},
            {
                "version": 1,
                "deployments": {
                    "https://xmc.example.com": {"access_token": "tok", "expires_at": True}
                },
            },
        ):
            self._write_credentials(bad_payload)
            with patch.dict(
                "os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True
            ):
                self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))

        # OSError during fstat closes the opened file descriptor
        with (
            patch("os.open", return_value=999),
            patch("os.fstat", side_effect=OSError("fstat failed")),
            patch("os.close") as mock_close,
            patch.dict("os.environ", {"XMC_CREDENTIALS_FILE": str(self.creds_path)}, clear=True),
            self.assertLogs("xdash.auth", level="WARNING"),
        ):
            self.assertIsNone(load_cached_cli_token("grpc.xmc.example.com:443"))
            mock_close.assert_called_once_with(999)

    def test_resolve_auth_headers_precedence_and_oidc_fallback(self) -> None:
        self._write_credentials(self._valid_payload("https://xmc.example.com", "cli-cached-token"))

        # 1. Explicit XMC_AUTH_TOKEN wins over cached CLI token
        with patch.dict(
            "os.environ",
            {
                "XMC_AUTH_TOKEN": "explicit-env-token",
                "XMC_CREDENTIALS_FILE": str(self.creds_path),
            },
            clear=True,
        ):
            headers = resolve_auth_headers("https://xmc.example.com/v1/dashboards")
            self.assertEqual(
                headers,
                {
                    "Content-Type": "application/json",
                    "Authorization": "Bearer explicit-env-token",
                },
            )

        # 2. Cached CLI token is used when env token is unset
        with patch.dict(
            "os.environ",
            {"XMC_CREDENTIALS_FILE": str(self.creds_path)},
            clear=True,
        ):
            headers = resolve_auth_headers(
                "https://xmc.example.com/v1/dashboards", content_type=None
            )
            self.assertEqual(headers, {"Authorization": "Bearer cli-cached-token"})

        # 3. IAM OIDC fallback when neither env token nor cached CLI token is available
        mock_google = MagicMock()
        mock_google.auth.default.return_value = (object(), "proj")
        mock_session = MagicMock()
        mock_session.post.return_value.json.return_value = {"token": "iam-fallback-token"}
        mock_google.auth.transport.requests.AuthorizedSession.return_value = mock_session

        with (
            patch.dict(
                "os.environ",
                {
                    "XMC_CREDENTIALS_FILE": str(pathlib.Path(self.temp_dir.name) / "missing.json"),
                    "IAP_CLIENT_ID": "iap-aud.apps.googleusercontent.com",
                    "XMC_CLIENT_SA": "sa@proj.iam.gserviceaccount.com",
                },
                clear=True,
            ),
            patch.dict(
                "sys.modules",
                {
                    "google": mock_google,
                    "google.auth": mock_google.auth,
                    "google.auth.transport": mock_google.auth.transport,
                    "google.auth.transport.requests": mock_google.auth.transport.requests,
                },
            ),
        ):
            headers = resolve_auth_headers("https://xmc.example.com/v1/dashboards")
            self.assertEqual(
                headers,
                {
                    "Content-Type": "application/json",
                    "Authorization": "Bearer iam-fallback-token",
                },
            )
            mock_session.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
