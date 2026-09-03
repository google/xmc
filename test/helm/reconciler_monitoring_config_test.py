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

"""Verifies the Helm chart renders reconciler monitoring and PodMonitoring correctly."""

import os
import shutil
import subprocess
import unittest
from typing import NoReturn

RECONCILER_CONFIGMAP = "templates/reconciler/configmap.yaml"
RECONCILER_DEPLOYMENT = "templates/reconciler/deployment.yaml"
RECONCILER_PODMONITORING = "templates/reconciler/podmonitoring.yaml"


def skip_unless_required(reason: str) -> NoReturn:
    """Skips the suite, unless XMC_REQUIRE_HELM is set."""
    if os.environ.get("XMC_REQUIRE_HELM", "").strip().lower() in ("1", "true", "yes"):
        raise RuntimeError(f"XMC_REQUIRE_HELM is set but {reason}")
    raise unittest.SkipTest(reason)


def find_chart_dir() -> str:
    """Locates deploy/helm from the working directory, supporting bazel runfiles and repo root."""
    for base in (os.curdir, *(os.path.join(*([os.pardir] * depth)) for depth in range(1, 4))):
        candidate = os.path.join(base, "deploy", "helm")
        if os.path.isfile(os.path.join(candidate, "Chart.yaml")):
            return candidate
    skip_unless_required("deploy/helm chart not found relative to working directory")


def render_template(show_only: str, values: dict[str, str] | None = None) -> str:
    """Renders a single chart template and returns the raw manifest text."""
    if values is None:
        values = {}
    overrides = [arg for key, value in values.items() for arg in ("--set", f"{key}={value}")]
    try:
        result = subprocess.run(
            ["helm", "template", "xmc", find_chart_dir(), "--show-only", show_only, *overrides],
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as error:
        raise RuntimeError(
            f"rendering {show_only} failed:\nstdout: {error.stdout}\nstderr: {error.stderr}"
        ) from error
    return result.stdout


class ReconcilerMonitoringConfigTest(unittest.TestCase):
    """Asserts reconciler monitoring configuration and PodMonitoring templates render correctly."""

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("helm") is None:
            skip_unless_required("helm binary not available")

    def test_default_configmap_metrics_port(self) -> None:
        """Default reconciler configmap must specify metrics_port 8082."""
        manifest = render_template(RECONCILER_CONFIGMAP)
        self.assertIn('metrics_port: "8082"', manifest)

    def test_custom_configmap_metrics_port(self) -> None:
        """Custom reconciler monitoring port must be reflected in configmap."""
        manifest = render_template(RECONCILER_CONFIGMAP, {"reconciler.monitoring.port": "9090"})
        self.assertIn('metrics_port: "9090"', manifest)

    def test_default_deployment_metrics_port(self) -> None:
        """Default reconciler deployment must expose containerPort 8082 named metrics."""
        manifest = render_template(RECONCILER_DEPLOYMENT)
        self.assertIn("- containerPort: 8082\n          name: metrics", manifest)

    def test_custom_deployment_metrics_port(self) -> None:
        """Custom reconciler monitoring port must be reflected in deployment containerPort."""
        manifest = render_template(RECONCILER_DEPLOYMENT, {"reconciler.monitoring.port": "9090"})
        self.assertIn("- containerPort: 9090\n          name: metrics", manifest)

    def test_podmonitoring_disabled_by_default(self) -> None:
        """PodMonitoring resource should not render when reconciler.monitoring.enabled is false."""
        manifest = render_template(RECONCILER_PODMONITORING)
        self.assertNotIn("kind: PodMonitoring", manifest)

    def test_podmonitoring_enabled(self) -> None:
        """PodMonitoring resource renders properly with configured interval when enabled."""
        manifest = render_template(
            RECONCILER_PODMONITORING,
            {
                "reconciler.monitoring.enabled": "true",
                "reconciler.monitoring.interval": "30s",
            },
        )
        self.assertIn("kind: PodMonitoring", manifest)
        self.assertIn("name: reconciler-monitoring", manifest)
        self.assertIn("port: metrics", manifest)
        self.assertIn("interval: 30s", manifest)

    def test_fallback_when_reconciler_block_is_null(self) -> None:
        """Templates safely fall back when reconciler or reconciler.monitoring is undefined."""
        manifest_cm = render_template(RECONCILER_CONFIGMAP, {"reconciler": "null"})
        self.assertIn('metrics_port: "8082"', manifest_cm)

        manifest_dep = render_template(RECONCILER_DEPLOYMENT, {"reconciler": "null"})
        self.assertIn("- containerPort: 8082\n          name: metrics", manifest_dep)

        manifest_pm = render_template(RECONCILER_PODMONITORING, {"reconciler": "null"})
        self.assertNotIn("kind: PodMonitoring", manifest_pm)


if __name__ == "__main__":
    unittest.main()
