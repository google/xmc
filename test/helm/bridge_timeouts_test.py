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

"""Checks that the xmc-bridge probe and reconcile timeouts are configured separately."""

import unittest

from chart_test_lib import render_template, require_helm

_CONFIGMAP = "templates/components/configmap.yaml"


class BridgeTimeoutsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_defaults(self) -> None:
        manifest = render_template(_CONFIGMAP, {"componentsDeployment.enabled": "true"})
        self.assertIn('PROBE_TIMEOUT: "2s"', manifest)
        self.assertIn('RECONCILE_TIMEOUT: "60s"', manifest)

    def test_reconcile_timeout_override(self) -> None:
        manifest = render_template(
            _CONFIGMAP,
            {
                "componentsDeployment.enabled": "true",
                "componentsDeployment.probeTimeout": "3s",
                "componentsDeployment.reconcileTimeout": "120s",
            },
        )
        self.assertIn('PROBE_TIMEOUT: "3s"', manifest)
        self.assertIn('RECONCILE_TIMEOUT: "120s"', manifest)


if __name__ == "__main__":
    unittest.main()
