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

"""Verifies the reconciler ConfigMap derives ui_base_url without operator configuration.

The reconciler stamps an experiment_url label on every MLRun it owns, and that label is only
useful if it is an absolute URL. ui_base_url is therefore derived from values the chart already
carries rather than being a field a platform engineer has to remember to set, and these cases
pin that derivation, its precedence, and the placeholder guard that keeps a bogus origin out of
the label.
"""

import re
import unittest

import chart_test_lib

RECONCILER_CONFIGMAP = "templates/reconciler/configmap.yaml"


def render_ui_base_url(values: dict[str, str]) -> str:
    """Renders the reconciler ConfigMap and returns the ui_base_url value."""
    manifest = chart_test_lib.render_template(RECONCILER_CONFIGMAP, values)
    match = re.search(r'^\s*ui_base_url:\s*"(.*)"\s*$', manifest, re.MULTILINE)
    if match is None:
        raise AssertionError(f"ui_base_url missing from rendered manifest:\n{manifest}")
    return match.group(1)


class ReconcilerUiBaseUrlConfigTest(unittest.TestCase):
    """Asserts ui_base_url derivation, precedence, and normalization."""

    @classmethod
    def setUpClass(cls) -> None:
        chart_test_lib.require_helm()

    def test_placeholder_values_yield_empty(self) -> None:
        """Stock placeholders must yield empty so no bogus origin is stamped on MLRun labels."""
        self.assertEqual(render_ui_base_url({}), "")

    def test_xmc_url_is_used(self) -> None:
        """xmcUrl is the documented top-level URL, so it supplies the origin."""
        self.assertEqual(
            render_ui_base_url({"xmcUrl": "xmc.example.com"}),
            "https://xmc.example.com",
        )

    def test_gateway_ui_hostname_is_used_when_xmc_url_unset(self) -> None:
        """Deployments predating xmcUrl still configure gateway.uiHostname."""
        self.assertEqual(
            render_ui_base_url({"gateway.uiHostname": "ui.corp.example"}),
            "https://ui.corp.example",
        )

    def test_environment_and_domain_suffix_fallback(self) -> None:
        """Charts carrying only environment/domainSuffix still produce a usable origin."""
        self.assertEqual(
            render_ui_base_url({"environment": "dev", "domainSuffix": "xmanageriso.dev"}),
            "https://dev.xmanageriso.dev",
        )

    def test_xmc_url_takes_precedence(self) -> None:
        """xmcUrl wins over every lower-precedence source."""
        self.assertEqual(
            render_ui_base_url(
                {
                    "xmcUrl": "win.example.com",
                    "gateway.uiHostname": "lose.example",
                    "environment": "dev",
                    "domainSuffix": "lose.dev",
                }
            ),
            "https://win.example.com",
        )

    def test_existing_scheme_is_preserved(self) -> None:
        """An origin that already carries a scheme must not be prefixed a second time."""
        self.assertEqual(
            render_ui_base_url({"xmcUrl": "http://xmc.example.com"}),
            "http://xmc.example.com",
        )

    def test_trailing_slash_is_trimmed(self) -> None:
        """The reconciler joins paths onto this value, so a trailing slash would double up."""
        self.assertEqual(
            render_ui_base_url({"xmcUrl": "https://xmc.example.com/"}),
            "https://xmc.example.com",
        )

    def test_placeholder_domain_suffix_does_not_leak(self) -> None:
        """An unsubstituted domainSuffix must not be composed into an origin."""
        self.assertEqual(render_ui_base_url({"environment": "dev"}), "")


if __name__ == "__main__":
    unittest.main()
