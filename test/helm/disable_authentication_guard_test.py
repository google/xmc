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

"""Checks that DISABLE_AUTHENTICATION can only be rendered for development environments."""

import unittest

from chart_test_lib import render_template, require_helm

_ESS_DEPLOYMENT = "templates/ess/deployment.yaml"


class DisableAuthenticationGuardTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_renders_when_allowed(self) -> None:
        cases = (
            ("authentication enabled in production", "false", "false"),
            ("authentication enabled in development", "false", "true"),
            ("authentication disabled in development", "true", "true"),
            # Non-empty strings are truthy in Go templates; the backend reads this as false.
            ("string no in production", "no", "false"),
        )
        for name, disabled, dev in cases:
            with self.subTest(name):
                manifest = render_template(
                    _ESS_DEPLOYMENT,
                    {
                        "essDeployment.enabled": "true",
                        "auth.disableAuthentication": disabled,
                        "isDevEnvironment": dev,
                    },
                )
                self.assertRegex(
                    manifest, rf'- name: DISABLE_AUTHENTICATION\n\s+value: "{disabled}"\n'
                )

    def test_fails_outside_development(self) -> None:
        # The backend reads DISABLE_AUTHENTICATION case-insensitively as true, 1 or yes.
        for flag in ("true", "True", "yes", "1"):
            with self.subTest(flag=flag):
                with self.assertRaisesRegex(
                    RuntimeError, "only allowed when isDevEnvironment=true"
                ):
                    render_template(
                        _ESS_DEPLOYMENT,
                        {
                            "essDeployment.enabled": "true",
                            "auth.disableAuthentication": flag,
                            "isDevEnvironment": "false",
                        },
                    )


if __name__ == "__main__":
    unittest.main()
