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

"""Verifies DoD requirements: zero external runtime dependencies and clean sys.modules."""

import sys
import unittest


class TestSDKSetupAndDependencies(unittest.TestCase):
    """Verifies DoD requirements: zero external runtime dependencies and clean sys.modules."""

    def test_sys_modules_purity(self) -> None:
        """Ensures importing xmc imports zero third-party packages into sys.modules."""
        forbidden_packages = {
            "requests",
            "pydantic",
            "httpx",
            "aiohttp",
            "urllib3",
            "certifi",
        }

        import xmc  # noqa: F401

        loaded_modules = set(sys.modules.keys())
        top_level_loaded = {mod.split(".")[0] for mod in loaded_modules}

        leaked_packages = forbidden_packages.intersection(top_level_loaded)
        self.assertFalse(
            leaked_packages,
            f"Forbidden external packages detected in sys.modules: {leaked_packages}",
        )

    def test_client_instantiation(self) -> None:
        """Verifies Client instantiates correctly without requiring network calls."""
        from xmc import Client

        client = Client(
            env={"XMC_GATEWAY_URL": "https://xmc.googleapis.com", "XMC_API_KEY": "test-key"}
        )
        self.assertEqual(client.base_url, "https://xmc.googleapis.com")
        self.assertEqual(client.api_key, "test-key")


if __name__ == "__main__":
    unittest.main()
