# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for dex_authenticator module."""

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock

from test.e2e.lib.config import (
    DEFAULT_ADMIN_PASSWORD,
    DEFAULT_ADMIN_USER,
    DEFAULT_BASE_URL,
    DEFAULT_DEX_URL,
)
from test.e2e.lib.dex_authenticator import DexAuthenticator


class TestDexAuthenticator(unittest.TestCase):
    """Tests for DexAuthenticator OIDC automation."""

    def setUp(self):
        self.auth = DexAuthenticator(
            base_url=DEFAULT_BASE_URL,
            dex_url=DEFAULT_DEX_URL,
        )

    def test_authenticate_invokes_full_flow(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.url = f"{DEFAULT_DEX_URL}/dex/auth"

            self.auth.authenticate(
                page=mock_page,
                email=DEFAULT_ADMIN_USER,
                password=DEFAULT_ADMIN_PASSWORD,
                screenshots_dir=Path(tmp_dir),
            )

            mock_page.goto.assert_called_once_with(
                DEFAULT_BASE_URL, wait_until="domcontentloaded", timeout=30_000
            )
            self.assertGreaterEqual(mock_page.locator.call_count, 3)

    def test_is_off_dex_identifies_dex_and_non_dex_urls(self):
        cases = [
            ("http://localhost:5556/dex/auth/local?back=&state=x", False),
            ("http://localhost:5556/dex/approval?req=x", False),
            ("http://localhost:8080/experiments", True),
            ("http://localhost:8080/?error=forbidden&email=unauthorized%40xmc.local", True),
            ("http://localhost:8080/?error=permission_error", True),
            ("http://localhost:8080/api/callback?code=a&state=b", True),
        ]
        for url, expected in cases:
            with self.subTest(url=url):
                self.assertEqual(self.auth._is_off_dex(url), expected)

    def test_credentials_wait_predicate_accepts_denial_redirect_and_approval(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.url = f"{DEFAULT_DEX_URL}/dex/auth/local"

            self.auth._fill_and_submit_credentials(
                page=mock_page,
                screenshots_dir=Path(tmp_dir),
                email="unauthorized@xmc.local",
                password="password123",
            )

            predicate = mock_page.wait_for_url.call_args[0][0]
            self.assertTrue(
                predicate("http://localhost:8080/?error=forbidden&email=unauthorized%40xmc.local")
            )
            self.assertTrue(predicate("http://localhost:8080/experiments"))
            self.assertTrue(predicate("http://localhost:5556/dex/approval?req=123"))
            self.assertFalse(predicate("http://localhost:5556/dex/auth/local?state=abc"))

    def test_credentials_wait_timeout_logs_warning_and_continues(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.url = f"{DEFAULT_DEX_URL}/dex/auth/local"
            mock_page.wait_for_url.side_effect = TimeoutError("wait_for_url timed out")

            with self.assertLogs("test.e2e.lib.dex_authenticator", level="WARNING") as cm:
                self.auth._fill_and_submit_credentials(
                    page=mock_page,
                    screenshots_dir=Path(tmp_dir),
                    email=DEFAULT_ADMIN_USER,
                    password=DEFAULT_ADMIN_PASSWORD,
                )
            self.assertTrue(any("Stalled" in msg for msg in cm.output))

    def test_wait_for_redirect_skips_when_already_off_dex(self):
        mock_page = MagicMock()
        mock_page.url = "http://localhost:8080/?error=forbidden"
        self.auth.wait_for_redirect(mock_page)
        mock_page.wait_for_url.assert_not_called()

    def test_handle_consent_noop_when_approval_absent(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.url = "http://localhost:8080/experiments"
            self.auth._handle_consent_if_present(mock_page, Path(tmp_dir), DEFAULT_ADMIN_USER)
            mock_page.locator.assert_not_called()


if __name__ == "__main__":
    unittest.main()
