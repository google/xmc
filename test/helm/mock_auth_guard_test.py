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

"""Checks that mock authentication cannot be rendered outside development environments."""

import unittest

from chart_test_lib import render_template, require_helm

_ESS_DEPLOYMENT = "templates/ess/deployment.yaml"


class MockAuthGuardTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_mock_auth_is_refused_outside_dev_environments(self) -> None:
        # The backend reads MOCK_AUTH case-insensitively as true, 1 or yes.
        for flag in ("true", "True", "yes", "1"):
            with self.subTest(flag=flag):
                values = {
                    "essDeployment.enabled": "true",
                    "auth.mockAuth": flag,
                    "isDevEnvironment": "false",
                }
                with self.assertRaisesRegex(
                    RuntimeError, "auth.mockAuth is only allowed when isDevEnvironment"
                ):
                    render_template(_ESS_DEPLOYMENT, values)

    def test_mock_auth_renders_in_dev_environments(self) -> None:
        values = {
            "essDeployment.enabled": "true",
            "auth.mockAuth": "true",
            "isDevEnvironment": "true",
        }
        manifest = render_template(_ESS_DEPLOYMENT, values)
        self.assertRegex(manifest, r'- name: MOCK_AUTH\n\s+value: "true"')

    def test_default_values_render(self) -> None:
        manifest = render_template(_ESS_DEPLOYMENT, {"essDeployment.enabled": "true"})
        self.assertRegex(manifest, r'- name: MOCK_AUTH\n\s+value: "false"')


if __name__ == "__main__":
    unittest.main()
