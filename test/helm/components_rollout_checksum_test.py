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

"""Helm template tests for per-service ConfigMap checksum rollout annotations."""

import re
import unittest

from chart_test_lib import render_template, require_helm

COMPONENT_AWARE_DEPLOYMENTS: tuple[tuple[str, str], ...] = (
    ("templates/ess/deployment.yaml", "experiment-state-server-deployment"),
    ("templates/dashboard-service/deployment.yaml", "dashboard-service-deployment"),
    ("templates/actuator/deployment.yaml", "actuator-deployment"),
    ("templates/health-service/deployment.yaml", "health-service-deployment"),
)

BASE_VALUES: dict[str, str] = {
    "dashboardServiceDeployment.enabled": "true",
    "essDeployment.enabled": "true",
    "healthServiceDeployment.enabled": "true",
}

POD_CONFIG_CHECKSUM_PATTERN = re.compile(
    r"template:\s*\n\s*metadata:\s*\n\s*annotations:(?:\s*\n\s*\S+:.*)*?\n\s*checksum/config:\s*([0-9a-f]{64})\b"
)


def pod_config_checksum(manifest: str) -> str:
    """Extracts the pod template metadata.annotations['checksum/config'] SHA-256 digest."""
    match = POD_CONFIG_CHECKSUM_PATTERN.search(manifest)
    if match is None:
        raise AssertionError(
            "pod template metadata.annotations['checksum/config'] SHA-256 digest not found"
        )
    return match.group(1)


class ComponentsRolloutChecksumTest(unittest.TestCase):
    """Verifies toggling componentsDeployment.enabled rotates pod checksum/config annotations."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_toggling_components_deployment_changes_pod_config_checksum(self) -> None:
        """Flipping componentsDeployment.enabled from false to true triggers a pod rollout."""
        disabled_values = {**BASE_VALUES, "componentsDeployment.enabled": "false"}
        enabled_values = {**BASE_VALUES, "componentsDeployment.enabled": "true"}

        for template, deployment_name in COMPONENT_AWARE_DEPLOYMENTS:
            with self.subTest(deployment=deployment_name, template=template):
                disabled_manifest = render_template(template, disabled_values)
                enabled_manifest = render_template(template, enabled_values)

                self.assertIn(f"name: {deployment_name}", disabled_manifest)
                self.assertIn(f"name: {deployment_name}", enabled_manifest)

                disabled_checksum = pod_config_checksum(disabled_manifest)
                enabled_checksum = pod_config_checksum(enabled_manifest)
                self.assertNotEqual(disabled_checksum, enabled_checksum)

    def test_pod_config_checksum_is_deterministic_for_identical_values(self) -> None:
        """Repeated renders with identical values produce the same checksum/config digest."""
        values = {**BASE_VALUES, "componentsDeployment.enabled": "true"}
        for template, deployment_name in COMPONENT_AWARE_DEPLOYMENTS:
            with self.subTest(deployment=deployment_name):
                first = pod_config_checksum(render_template(template, values))
                second = pod_config_checksum(render_template(template, values))
                self.assertEqual(first, second)

    def test_null_components_and_metrics_blocks_render_safely(self) -> None:
        """Null overrides for componentsDeployment or metrics do not fail checksum rendering."""
        values = {**BASE_VALUES, "componentsDeployment": "null", "metrics": "null"}
        for template, deployment_name in COMPONENT_AWARE_DEPLOYMENTS:
            with self.subTest(deployment=deployment_name):
                manifest = render_template(template, values)
                self.assertEqual(len(pod_config_checksum(manifest)), 64)


if __name__ == "__main__":
    unittest.main()
