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

"""Verifies the Reconciler ConfigMap only carries redis_addr when artifact lifecycle is enabled."""

import unittest

from chart_test_lib import render_template, require_helm

RECONCILER_CONFIGMAP = "templates/reconciler/configmap.yaml"
FLAG = "featureFlags.artifact_lifecycle_enabled"
REDIS_ADDR = "10.0.0.5:6379"


class ReconcilerRedisAddrTest(unittest.TestCase):
    """Asserts redis_addr follows the artifact lifecycle feature flag, matching main when off."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_redis_addr_follows_flag(self) -> None:
        """redis_addr renders only when the flag is on."""
        cases: tuple[tuple[str, dict[str, str], bool], ...] = (
            ("chart default", {}, False),
            ("flag off", {FLAG: "false"}, False),
            ("flag on", {FLAG: "true"}, True),
        )
        for name, overrides, want_redis in cases:
            with self.subTest(name):
                manifest = render_template(
                    RECONCILER_CONFIGMAP, {"redisAddr": REDIS_ADDR, **overrides}
                )
                self.assertEqual(want_redis, f'redis_addr: "{REDIS_ADDR}"' in manifest)
                self.assertEqual(want_redis, "redis_addr:" in manifest)


if __name__ == "__main__":
    unittest.main()
