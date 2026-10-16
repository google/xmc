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

"""Checks the cluster identity values that xmc-bridge declares to the CMS ownership fence."""

import unittest

from chart_test_lib import render_template, require_helm

_CONFIGMAP = "templates/components/configmap.yaml"


class BridgeClusterIdentityTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_defaults(self) -> None:
        manifest = render_template(_CONFIGMAP, {"componentsDeployment.enabled": "true"})
        self.assertIn('CLUSTER_NAME: "xmc"', manifest)
        self.assertIn('COMPONENT_OWNERSHIP_EPOCH: "1"', manifest)

    def test_overrides(self) -> None:
        manifest = render_template(
            _CONFIGMAP,
            {
                "componentsDeployment.enabled": "true",
                "componentsDeployment.clusterName": "prod-east",
                "componentsDeployment.ownershipEpoch": "1700000000",
            },
        )
        self.assertIn('CLUSTER_NAME: "prod-east"', manifest)
        self.assertIn('COMPONENT_OWNERSHIP_EPOCH: "1700000000"', manifest)

    def test_ownership_epoch_explicit_zero_is_not_defaulted(self) -> None:
        # xmc-bridge rejects epochs below 1 at startup; the chart must pass 0 through rather than render 1.
        manifest = render_template(
            _CONFIGMAP,
            {
                "componentsDeployment.enabled": "true",
                "componentsDeployment.ownershipEpoch": "0",
            },
        )
        self.assertIn('COMPONENT_OWNERSHIP_EPOCH: "0"', manifest)


if __name__ == "__main__":
    unittest.main()
