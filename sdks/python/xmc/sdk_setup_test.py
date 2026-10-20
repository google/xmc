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

import os
import subprocess
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

    def test_auth_cli_import_needs_no_protos(self) -> None:
        """Importing the `xmc login` CLI loads neither protobuf nor api.protos."""
        script = (
            "import sys, xmc.auth.cli; "
            "print(sorted(m for m in sys.modules if m.split('.')[0] in ('google', 'api')))"
        )
        env = {**os.environ, "PYTHONPATH": os.pathsep.join(sys.path)}
        result = subprocess.run(
            [sys.executable, "-c", script], capture_output=True, text=True, env=env, check=True
        )
        self.assertEqual(result.stdout.strip(), "[]")

    def test_public_attributes_resolve(self) -> None:
        """Every name in xmc.__all__, including lazily imported ones, resolves."""
        import xmc
        from xmc.types import Metric

        for name in xmc.__all__:
            with self.subTest(name=name):
                self.assertIsNotNone(getattr(xmc, name))
        self.assertIs(xmc.Metric, Metric)
        with self.assertRaises(AttributeError):
            _ = xmc.does_not_exist


if __name__ == "__main__":
    unittest.main()
