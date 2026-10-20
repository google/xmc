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

"""Checks that the OIDC email policy reaches every service that authenticates users."""

import re
import unittest

from chart_test_lib import render_template, require_helm

# Every deployment that includes xmanager-cloud.oauthEnv, with the value that enables it.
_OAUTH_SERVICES = {
    "templates/components/deployment.yaml": "componentsDeployment.enabled",
    "templates/dashboard-service/deployment.yaml": "dashboardServiceDeployment.enabled",
    "templates/ess/deployment.yaml": "essDeployment.enabled",
    "templates/health-service/deployment.yaml": "healthServiceDeployment.enabled",
    "templates/rod/deployment.yaml": "rodDeployment.enabled",
    "templates/xid/deployment.yaml": None,
}


class OIDCEmailPolicyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_email_policy_rendered(self) -> None:
        entra_keys = "https://login.microsoftonline.com/contoso.onmicrosoft.com/discovery/v2.0/keys"
        cases = {
            "default": ({}, "require", "email", ""),
            "entra upn": (
                {
                    "auth.oidc.emailVerification": "trust-issuer",
                    "auth.oidc.emailClaim": "upn",
                    "auth.jwksUrl": entra_keys,
                },
                "trust-issuer",
                "upn",
                entra_keys,
            ),
            "jwks url from issuer": (
                {"auth.issuerUrl": "https://dex.example.com/"},
                "require",
                "email",
                "https://dex.example.com/keys",
            ),
            "oidc block removed": ({"auth.oidc": "null"}, "require", "email", ""),
        }
        for template, enable_key in _OAUTH_SERVICES.items():
            for case, (overrides, verification, claim, jwks_url) in cases.items():
                with self.subTest(template=template, case=case):
                    values = {enable_key: "true"} if enable_key else {}
                    values.update(overrides)
                    manifest = render_template(template, values)
                    self.assertRegex(
                        manifest, rf'- name: OIDC_EMAIL_VERIFICATION\n\s+value: "{verification}"'
                    )
                    self.assertRegex(manifest, rf'- name: OIDC_EMAIL_CLAIM\n\s+value: "{claim}"')
                    self.assertRegex(
                        manifest, rf'- name: OIDC_JWKS_URL\n\s+value: "{re.escape(jwks_url)}"'
                    )

    def test_configmaps_share_the_jwks_url(self) -> None:
        configmaps = {
            "templates/actuator/configmap.yaml": "jwks_url",
            "templates/ess/configmap.yaml": "ess_jwks_url",
            "templates/xid/configmap.yaml": "xid_jwks_url",
        }
        cases = {
            "unset": ({}, ""),
            "explicit": (
                {"auth.jwksUrl": "https://idp.example.com/jwks"},
                "https://idp.example.com/jwks",
            ),
            "from issuer": (
                {"auth.issuerUrl": "https://dex.example.com"},
                "https://dex.example.com/keys",
            ),
        }
        for template, key in configmaps.items():
            for case, (overrides, expected) in cases.items():
                with self.subTest(template=template, case=case):
                    manifest = render_template(template, overrides)
                    self.assertRegex(manifest, rf'{key}: "{re.escape(expected)}"')


if __name__ == "__main__":
    unittest.main()
