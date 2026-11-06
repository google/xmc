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

"""Verifies actuator OIDC settings and the shared ESS-to-actuator OIDC audience contract."""

import re
import unittest

from chart_test_lib import render_template, require_helm

ACTUATOR_CONFIGMAP = "templates/actuator/configmap.yaml"
ESS_CONFIGMAP = "templates/ess/configmap.yaml"
CLUSTER_LOCAL_ACTUATOR_URI = "https://actuator.xmc.svc.cluster.local/actuate"


def _configmap_data_value(manifest: str, key: str) -> str:
    """Extracts a double-quoted top-level data key from a rendered ConfigMap manifest."""
    match = re.search(rf'^\s{{2}}{re.escape(key)}:\s*"([^"]*)"\s*$', manifest, re.MULTILINE)
    if match is None:
        raise AssertionError(f"ConfigMap data key {key!r} not found in manifest:\n{manifest}")
    return match.group(1)


class ActuatorDevOidcConfigTest(unittest.TestCase):
    """Asserts dev-only OIDC settings in actuator-config and the shared audience in ess-config."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_dev_environment_includes_oidc_client_id_and_username(self) -> None:
        """Dev environment actuator-config must include CLIENT_ID and OIDC_USERNAME."""
        manifest = render_template(
            ACTUATOR_CONFIGMAP,
            {
                "isDevEnvironment": "true",
                "auth.clientId": "xmc-client",
                "auth.username": "admin@xmc.local",
            },
        )
        self.assertIn('CLIENT_ID: "xmc-client"', manifest)
        self.assertIn('OIDC_USERNAME: "admin@xmc.local"', manifest)

    def test_non_dev_environment_omits_oidc_client_id_and_username(self) -> None:
        """Production actuator-config must omit dev-only CLIENT_ID and OIDC_USERNAME."""
        manifest = render_template(
            ACTUATOR_CONFIGMAP,
            {
                "isDevEnvironment": "false",
                "auth.clientId": "xmc-client",
                "auth.username": "admin@xmc.local",
            },
        )
        self.assertNotIn("CLIENT_ID:", manifest)
        self.assertNotIn("OIDC_USERNAME:", manifest)

    def test_dev_environment_with_missing_auth_fields_renders_empty_strings(self) -> None:
        """actuator-config must render empty strings when auth.clientId and auth.username are unset."""
        manifest = render_template(
            ACTUATOR_CONFIGMAP,
            {
                "isDevEnvironment": "true",
            },
        )
        self.assertIn('CLIENT_ID: ""', manifest)
        self.assertIn('OIDC_USERNAME: ""', manifest)

    def test_actuator_and_ess_configmaps_use_cluster_local_actuator_uri(self) -> None:
        """actuator-config audience and ess-config ess_actuator_url must match the cluster-local URI."""
        actuator_manifest = render_template(ACTUATOR_CONFIGMAP, {})
        ess_manifest = render_template(ESS_CONFIGMAP, {"essDeployment.enabled": "true"})
        audience = _configmap_data_value(actuator_manifest, "audience")
        ess_actuator_url = _configmap_data_value(ess_manifest, "ess_actuator_url")

        self.assertEqual(audience, ess_actuator_url)
        self.assertEqual(audience, CLUSTER_LOCAL_ACTUATOR_URI)
        self.assertNotIn("actuator.api.", actuator_manifest)
        self.assertNotIn("actuator.api.", ess_manifest)


if __name__ == "__main__":
    unittest.main()
