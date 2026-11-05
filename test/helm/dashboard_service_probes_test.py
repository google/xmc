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

"""Verifies the dashboard service probes match what cmd/dashboard_service actually implements.

Readiness reports on Spanner and, once the metrics pipeline is enabled, on the ClickHouse
connection behind the chart data engine. The handler bounds that work with readinessTimeout, so
the probe has to allow at least as long or a healthy-but-slow dependency reads as a failure.
Liveness must stay on the endpoint that checks nothing, because restarting the container cannot
repair a dependency and only throws away a warm process.
"""

import unittest

from chart_test_lib import render_template, require_helm

DASHBOARD_DEPLOYMENT = "templates/dashboard-service/deployment.yaml"

# Mirrors readinessTimeout in cmd/dashboard_service/main.go, in seconds.
READINESS_TIMEOUT_SECONDS = 5


def probe_settings(manifest: str, probe_name: str) -> dict[str, str]:
    """Returns the settings under one probe block of a rendered deployment.

    Nested keys are flattened, so httpGet.path arrives as "path". The manifests are small and
    uniformly indented, which makes reading the block line by line cheaper than teaching the CI
    runner about a YAML parser it needs for nothing else.
    """
    lines = manifest.splitlines()
    starts = [i for i, line in enumerate(lines) if line.strip() == f"{probe_name}:"]
    if not starts:
        raise AssertionError(f"{probe_name} is missing from the rendered deployment")

    settings: dict[str, str] = {}
    indent = len(lines[starts[0]]) - len(lines[starts[0]].lstrip())
    for line in lines[starts[0] + 1 :]:
        if len(line) - len(line.lstrip()) <= indent:
            break
        key, _, value = line.strip().partition(": ")
        if value:
            settings[key] = value
    return settings


class DashboardServiceProbesTest(unittest.TestCase):
    """Asserts the kubelet probes agree with the handlers behind them."""

    manifest: str

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()
        cls.manifest = render_template(
            DASHBOARD_DEPLOYMENT, {"dashboardServiceDeployment.enabled": "true"}
        )

    def test_readiness_probe_targets_the_readiness_endpoint(self) -> None:
        self.assertEqual(probe_settings(self.manifest, "readinessProbe")["path"], "/readyz")

    def test_readiness_probe_outlasts_the_handler_budget(self) -> None:
        """A timeout below the handler budget fails the probe on latency alone."""
        timeout = int(probe_settings(self.manifest, "readinessProbe")["timeoutSeconds"])

        self.assertGreater(timeout, READINESS_TIMEOUT_SECONDS)

    def test_readiness_probe_timeout_fits_inside_its_period(self) -> None:
        """Overlapping probes would queue behind each other against a slow dependency."""
        probe = probe_settings(self.manifest, "readinessProbe")

        self.assertLess(int(probe["timeoutSeconds"]), int(probe["periodSeconds"]))

    def test_readiness_probe_tolerates_a_transient_failure(self) -> None:
        """One slow dependency check must not immediately drop the pod from the service."""
        threshold = int(probe_settings(self.manifest, "readinessProbe")["failureThreshold"])

        self.assertGreater(threshold, 1)

    def test_liveness_probe_does_not_check_dependencies(self) -> None:
        """Pointing liveness at /readyz would restart the pod for every ClickHouse outage."""
        self.assertEqual(probe_settings(self.manifest, "livenessProbe")["path"], "/healthz")


if __name__ == "__main__":
    unittest.main()
