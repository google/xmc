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

"""Verifies the reconciler ConfigMap forwards the XManager UI hostname.

The reconciler base32-encodes this host into the xmanager_base32_url label on every
MLRun it owns, so a consumer can rebuild a link back to the experiment. The value is
derived from chart values an operator already sets, so enabling it requires no extra
platform configuration.

The chart deliberately forwards the host verbatim, including unsubstituted
placeholders: rejecting values that are not real hostnames is the reconciler's job,
so that the label contract lives in exactly one place.
"""

import re
import unittest

import chart_test_lib

CONFIGMAP = "templates/reconciler/configmap.yaml"
KEY = "xmanager_ui_host"


def render_ui_host(values: dict[str, str]) -> str:
    """Renders the reconciler ConfigMap and returns the xmanager_ui_host value."""
    manifest = chart_test_lib.render_template(CONFIGMAP, values)
    match = re.search(rf'^\s*{KEY}:\s*"(.*)"\s*$', manifest, re.MULTILINE)
    if match is None:
        raise AssertionError(f"{KEY} missing from rendered manifest:\n{manifest}")
    return match.group(1)


class ReconcilerXManagerIdentityConfigTest(unittest.TestCase):
    """Asserts the UI host is always emitted and mirrors the chart's hostname helper."""

    @classmethod
    def setUpClass(cls) -> None:
        chart_test_lib.require_helm()

    def test_uses_xmc_url_when_set(self) -> None:
        """xmcUrl is the canonical UI host and wins outright."""
        self.assertEqual(
            render_ui_host({"xmcUrl": "yinghong5.dev.xmanageriso.dev"}),
            "yinghong5.dev.xmanageriso.dev",
        )

    def test_falls_back_to_gateway_ui_hostname(self) -> None:
        """Without xmcUrl the chart falls back to the gateway hostname."""
        self.assertEqual(
            render_ui_host({"gateway.uiHostname": "xmc.example.com"}),
            "xmc.example.com",
        )

    def test_key_is_always_present(self) -> None:
        """The key is emitted even when unset so the reconciler sees a stable contract."""
        self.assertEqual(render_ui_host({"gateway.uiHostname": ""}), "")

    def test_chart_does_not_normalize_the_host(self) -> None:
        """Normalization belongs to the reconciler, which owns the label contract."""
        self.assertEqual(
            render_ui_host({"xmcUrl": "YOUR_UI_HOSTNAME"}),
            "YOUR_UI_HOSTNAME",
        )
