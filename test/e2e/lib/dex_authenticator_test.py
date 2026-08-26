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

from test.e2e.lib.dex_authenticator import DexAuthenticator


class TestDexAuthenticator(unittest.TestCase):
    """Tests for DexAuthenticator OIDC automation."""

    def setUp(self):
        self.auth = DexAuthenticator(
            base_url="http://localhost:8080",
            dex_url="http://localhost:5556",
        )

    def test_authenticate_invokes_full_flow(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.url = "http://localhost:5556/dex/auth"

            self.auth.authenticate(
                page=mock_page,
                email="admin@xmc.local",
                password="password123",
                screenshots_dir=Path(tmp_dir),
            )

            mock_page.goto.assert_called_once_with("http://localhost:8080")
            self.assertGreaterEqual(mock_page.locator.call_count, 3)


if __name__ == "__main__":
    unittest.main()
