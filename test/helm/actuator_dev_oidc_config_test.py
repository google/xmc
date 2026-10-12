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

"""Verifies actuator-config renders dev OIDC settings only when isDevEnvironment is true."""

import unittest

from chart_test_lib import render_template, require_helm

ACTUATOR_CONFIGMAP = "templates/actuator/configmap.yaml"


class ActuatorDevOidcConfigTest(unittest.TestCase):
    """Asserts CLIENT_ID and OIDC_USERNAME render in actuator-config only for dev environments."""

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


if __name__ == "__main__":
    unittest.main()
