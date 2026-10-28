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

"""Verifies the Reconciler only requires redis-secret when artifact lifecycle is enabled."""

import unittest

from chart_test_lib import render_template, require_helm

RECONCILER_DEPLOYMENT = "templates/reconciler/deployment.yaml"
FLAG = "featureFlags.artifact_lifecycle_enabled"


class ReconcilerRedisSecretTest(unittest.TestCase):
    """Asserts the REDIS_PASSWORD env var follows the artifact lifecycle feature flag."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_redis_password_follows_flag(self) -> None:
        """REDIS_PASSWORD and redis-secret render only when the flag is on."""
        cases: tuple[tuple[str, dict[str, str], bool], ...] = (
            ("chart default", {}, False),
            ("flag off", {FLAG: "false"}, False),
            ("flag on", {FLAG: "true"}, True),
        )
        for name, values, want_redis in cases:
            with self.subTest(name):
                manifest = render_template(RECONCILER_DEPLOYMENT, values)
                for needle in ("name: REDIS_PASSWORD", "name: redis-secret"):
                    self.assertEqual(want_redis, needle in manifest, needle)

    def test_flag_off_renders_no_env_without_other_entries(self) -> None:
        """With the flag off and no other env entries, the env block is omitted as on main."""
        manifest = render_template(RECONCILER_DEPLOYMENT, {FLAG: "false"})
        self.assertNotIn("        env:\n", manifest)

    def test_flag_on_keeps_other_env_entries(self) -> None:
        """Enabling the flag keeps GOOGLE_APPLICATION_CREDENTIALS alongside REDIS_PASSWORD."""
        manifest = render_template(
            RECONCILER_DEPLOYMENT,
            {FLAG: "true", "googleApplicationCredentials": "/var/secrets/key.json"},
        )
        self.assertIn("name: REDIS_PASSWORD", manifest)
        self.assertIn('value: "/var/secrets/key.json"', manifest)


if __name__ == "__main__":
    unittest.main()
