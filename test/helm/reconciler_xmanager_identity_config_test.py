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

"""Verifies the reconciler ConfigMap forwards the XManager deployment identifiers.

The reconciler stamps xmanager_env_name and xmanager_domain_name onto every MLRun it owns
so consumers can resolve a run back to the deployment that produced it. Both values are
plain pass-throughs of chart values an operator already sets, so no extra configuration is
required. The reconciler is responsible for dropping values that are not label-safe, which
is why the chart is allowed to forward placeholders verbatim.
"""

import re
import unittest

import chart_test_lib

CONFIGMAP = "templates/reconciler/configmap.yaml"


def render_identity(values: dict[str, str]) -> dict[str, str]:
    """Renders the reconciler ConfigMap and returns the two identifier values."""
    manifest = chart_test_lib.render_template(CONFIGMAP, values)
    found = {}
    for key in ("xmanager_env_name", "xmanager_domain_name"):
        match = re.search(rf'^\s*{key}:\s*"(.*)"\s*$', manifest, re.MULTILINE)
        if match is None:
            raise AssertionError(f"{key} missing from rendered manifest:\n{manifest}")
        found[key] = match.group(1)
    return found


class ReconcilerXManagerIdentityConfigTest(unittest.TestCase):
    """Asserts the identifiers are always emitted and always mirror chart values."""

    @classmethod
    def setUpClass(cls) -> None:
        chart_test_lib.require_helm()

    def test_forwards_environment_and_domain_suffix(self) -> None:
        """A configured deployment forwards both values verbatim."""
        self.assertEqual(
            render_identity({"environment": "yinghong5", "domainSuffix": "dev.xmanageriso.dev"}),
            {
                "xmanager_env_name": "yinghong5",
                "xmanager_domain_name": "dev.xmanageriso.dev",
            },
        )

    def test_keys_are_always_present(self) -> None:
        """Both keys render even on stock values, so the reconciler env is stable."""
        rendered = render_identity({})
        self.assertIn("xmanager_env_name", rendered)
        self.assertIn("xmanager_domain_name", rendered)

    def test_environment_only(self) -> None:
        """Setting just the environment still renders a well-formed ConfigMap."""
        self.assertEqual(render_identity({"environment": "dev"})["xmanager_env_name"], "dev")

    def test_values_are_not_rewritten_by_the_chart(self) -> None:
        """The chart must not normalize: the reconciler owns label-safety filtering."""
        self.assertEqual(
            render_identity({"environment": "Mixed_Case", "domainSuffix": "Example.COM"}),
            {
                "xmanager_env_name": "Mixed_Case",
                "xmanager_domain_name": "Example.COM",
            },
        )


if __name__ == "__main__":
    unittest.main()
