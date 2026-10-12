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

"""Verifies GKE Gateway HTTPRoute routing configurations for microservices."""

import unittest

from chart_test_lib import render_template, require_helm


class HTTPRouteConfigTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_rod_httproute_omitted_when_disabled(self) -> None:
        """The rod HTTPRoute should not be rendered when rodDeployment or gateway is disabled."""
        for values in (
            {"gateway.enabled": "false", "rodDeployment.enabled": "true"},
            {"gateway.enabled": "true", "rodDeployment.enabled": "false"},
            {"gateway.enabled": "false", "rodDeployment.enabled": "false"},
        ):
            with self.subTest(values=values):
                manifest = render_template("templates/rod/httproute.yaml", values)
                self.assertNotIn("kind: HTTPRoute", manifest)
                self.assertNotIn("name: rod-route", manifest)

    def test_rod_httproute_matches_clusterqueues_prefix_and_batch_get_secondary_data_exact(
        self,
    ) -> None:
        """Verifies rod-route matches /v1/clusterqueues (PathPrefix) and AIP-136 custom verb (Exact).

        Kubernetes Gateway API PathPrefix matches slash-delimited segments only.
        Custom verbs like /v1/clusterqueues:batchGetSecondaryData require an Exact match
        rule to avoid falling through to the generic /v1 UI route.
        """
        values = {
            "gateway.enabled": "true",
            "rodDeployment.enabled": "true",
        }
        manifest = render_template("templates/rod/httproute.yaml", values)

        self.assertIn("kind: HTTPRoute", manifest)
        self.assertIn("name: rod-route", manifest)

        # Verify PathPrefix match for /v1/clusterqueues.
        self.assertRegex(
            manifest,
            r"-\s+path:\s*\n\s+type:\s+PathPrefix\s*\n\s+value:\s+/v1/clusterqueues",
        )

        # Verify Exact match for /v1/clusterqueues:batchGetSecondaryData.
        self.assertRegex(
            manifest,
            r"-\s+path:\s*\n\s+type:\s+Exact\s*\n\s+value:\s+/v1/clusterqueues:batchGetSecondaryData",
        )

        # Verify backendRef routes to rod-data-provider-service on port 8080.
        self.assertRegex(
            manifest,
            r"backendRefs:\s*\n\s+-\s+name:\s+rod-data-provider-service\s*\n\s+port:\s+8080",
        )


if __name__ == "__main__":
    unittest.main()
