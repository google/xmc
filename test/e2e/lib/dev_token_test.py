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

"""Unit tests for dev_token module."""

from __future__ import annotations

import json
import unittest
from unittest.mock import MagicMock, patch

from test.e2e.lib.config import (
    DEFAULT_DEV_EMAIL,
    DEFAULT_DEV_PASSWORD,
    DEFAULT_DEX_URL,
)
from test.e2e.lib.dev_token import (
    fetch_dex_oidc_token,
)


class TestDevToken(unittest.TestCase):
    """Tests for centralized Dex OIDC token retrieval."""

    @patch("urllib.request.urlopen")
    def test_fetch_dex_oidc_token_success(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps(
            {
                "id_token": "mock.rs256.jwt.token",
                "access_token": "mock.access.token",
                "token_type": "bearer",
                "expires_in": 86400,
            }
        ).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_response

        token = fetch_dex_oidc_token(
            dex_url=DEFAULT_DEX_URL,
            email=DEFAULT_DEV_EMAIL,
            password=DEFAULT_DEV_PASSWORD,
        )
        self.assertEqual(token, "mock.rs256.jwt.token")

        # Verify request details
        call_args = mock_urlopen.call_args[0]
        req = call_args[0]
        self.assertEqual(req.full_url, f"{DEFAULT_DEX_URL}/dex/token")
        self.assertEqual(req.get_method(), "POST")

    @patch("urllib.request.urlopen")
    def test_fetch_dex_oidc_token_defaults(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps(
            {
                "id_token": "default.rs256.jwt",
            }
        ).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_response

        token = fetch_dex_oidc_token()
        self.assertEqual(token, "default.rs256.jwt")

    @patch("urllib.request.urlopen")
    def test_fetch_dex_oidc_token_missing_token_in_response(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"error": "invalid_grant"}).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_response

        with self.assertRaises(RuntimeError):
            fetch_dex_oidc_token()


if __name__ == "__main__":
    unittest.main()
