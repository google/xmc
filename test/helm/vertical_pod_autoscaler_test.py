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

"""Verifies the VerticalPodAutoscaler objects can be switched off for clusters without the CRDs.

VerticalPodAutoscaler lives in autoscaling.k8s.io, which GKE installs only on clusters created
with Vertical Pod Autoscaling enabled. The greenfield blueprint always enables it, so the default
has to keep rendering both objects. A pre-existing cluster may not have it, and there the release
fails on "no matches for kind" before anything else installs, so the gate has to remove the
objects without disturbing the deployments they target.
"""

import unittest

from chart_test_lib import render_chart, require_helm

VPA_KIND = "kind: VerticalPodAutoscaler"

ACTUATOR_VPA_NAME = "name: actuator-vpa"
RECONCILER_VPA_NAME = "name: reconciler-vpa"

ACTUATOR_DEPLOYMENT_NAME = "name: actuator-deployment"
RECONCILER_DEPLOYMENT_NAME = "name: reconciler-deployment"


class VerticalPodAutoscalerGateTest(unittest.TestCase):
    """Asserts the autoscaler objects follow verticalPodAutoscaler.enabled."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()
        cls.enabled = render_chart({})
        cls.disabled = render_chart({"verticalPodAutoscaler.enabled": "false"})

    def test_actuator_autoscaler_renders_by_default(self) -> None:
        """The greenfield blueprint enables VPA on the cluster, so the default must not change."""
        self.assertIn(ACTUATOR_VPA_NAME, self.enabled)

    def test_reconciler_autoscaler_renders_by_default(self) -> None:
        self.assertIn(RECONCILER_VPA_NAME, self.enabled)

    def test_no_autoscaler_objects_when_disabled(self) -> None:
        """Any surviving object fails the install on a cluster without the autoscaling CRDs."""
        self.assertNotIn(VPA_KIND, self.disabled)

    def test_actuator_deployment_survives_disabling(self) -> None:
        """A gate that swallowed the deployment would break the workload it was meant to spare."""
        self.assertIn(ACTUATOR_DEPLOYMENT_NAME, self.disabled)

    def test_reconciler_deployment_survives_disabling(self) -> None:
        self.assertIn(RECONCILER_DEPLOYMENT_NAME, self.disabled)


if __name__ == "__main__":
    unittest.main()
