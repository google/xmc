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

"""Verifies the Helm chart passes every ClickHouse setting the dashboard service requires.

The dashboard service builds its chart data engine at startup from the environment variables
read by pkg/clickhouse.GetOptions. When any are missing the pod still starts and serves
dashboard CRUD, so a missing entry stays invisible until GetChartData fails at request time.
These tests render the chart and assert the full set is wired.
"""

import os
import re
import shutil
import subprocess
import unittest
from typing import NoReturn
from unittest import mock

# Environment variables read by pkg/clickhouse.GetOptions. CLICKHOUSE_DSN is deliberately
# excluded: it is an alternative to these and the chart does not render it.
REQUIRED_CLICKHOUSE_ENV_VARS = frozenset(
    {
        "CLICKHOUSE_HOST",
        "CLICKHOUSE_PORT",
        "CLICKHOUSE_DATABASE",
        "CLICKHOUSE_USER",
        "CLICKHOUSE_PASSWORD",
    }
)

DASHBOARD_DEPLOYMENT = "templates/dashboard-service/deployment.yaml"
DASHBOARD_CONFIGMAP = "templates/dashboard-service/configmap.yaml"
CLICKHOUSE_SECRET = "templates/metrics/clickhouse-secret.yaml"

ENV_VAR_NAME_PATTERN = re.compile(r"^\s*- name: (CLICKHOUSE_\w+)$", re.MULTILINE)


def skip_unless_required(reason: str) -> NoReturn:
    """Skips the suite, unless the caller declared the prerequisites are supposed to be present.

    A developer without helm installed should not be blocked, but CI installs it deliberately.
    Skipping there would report a green run for tests that never executed, which is exactly the
    silent gap this suite exists to close, so XMC_REQUIRE_HELM turns the skip into a failure.
    """
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


def render_template(show_only: str, values: dict[str, str]) -> str:
    """Renders a single chart template and returns the raw manifest text."""
    overrides = [arg for key, value in values.items() for arg in ("--set", f"{key}={value}")]
    try:
        result = subprocess.run(
            ["helm", "template", "xmc", find_chart_dir(), "--show-only", show_only, *overrides],
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as error:
        # CalledProcessError omits captured output, which hides the template error that
        # explains the failure.
        raise RuntimeError(
            f"rendering {show_only} failed:\nstdout: {error.stdout}\nstderr: {error.stderr}"
        ) from error
    return result.stdout


def rendered_clickhouse_env_vars(manifest: str) -> set[str]:
    """Returns the names of every CLICKHOUSE_* environment variable in a rendered manifest."""
    return set(ENV_VAR_NAME_PATTERN.findall(manifest))


class SkipUnlessRequiredTest(unittest.TestCase):
    """Covers the guard that stops a missing helm binary from silently greening a CI run."""

    def test_skips_when_prerequisites_are_optional(self) -> None:
        with mock.patch.dict(os.environ):
            os.environ.pop("XMC_REQUIRE_HELM", None)

            with self.assertRaises(unittest.SkipTest):
                skip_unless_required("helm binary not available")

    def test_raises_when_prerequisites_are_required(self) -> None:
        # A workflow may spell the flag as a YAML boolean, which reaches the process as "true", so
        # every common truthy spelling has to arm the guard rather than silently skip the suite.
        for value in ("1", "true", "TRUE", "yes", " True "):
            with self.subTest(value=value):
                with mock.patch.dict(os.environ, {"XMC_REQUIRE_HELM": value}):
                    with self.assertRaises(RuntimeError):
                        skip_unless_required("helm binary not available")

    def test_skips_when_the_flag_is_not_truthy(self) -> None:
        for value in ("", "0", "false", "no"):
            with self.subTest(value=value):
                with mock.patch.dict(os.environ, {"XMC_REQUIRE_HELM": value}):
                    with self.assertRaises(unittest.SkipTest):
                        skip_unless_required("helm binary not available")


class DashboardServiceClickhouseConfigTest(unittest.TestCase):
    """Asserts the dashboard service deployment receives complete ClickHouse configuration."""

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("helm") is None:
            skip_unless_required("helm binary not available")

    def test_all_required_clickhouse_vars_are_passed_when_metrics_enabled(self) -> None:
        """Every variable pkg/clickhouse reads must reach the container."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {"dashboardServiceDeployment.enabled": "true", "metrics.enabled": "true"},
        )

        missing = REQUIRED_CLICKHOUSE_ENV_VARS - rendered_clickhouse_env_vars(manifest)

        self.assertEqual(
            missing, set(), f"dashboard service is missing ClickHouse config: {missing}"
        )

    def test_clickhouse_vars_reflect_configured_values(self) -> None:
        """Operator overrides must reach the container rather than silently falling back."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.enabled": "true",
                "metrics.clickhouse.host": "ch.example.internal",
                "metrics.clickhouse.port": "9440",
                "metrics.clickhouse.database": "xmc",
                "metrics.clickhouse.user": "reader",
            },
        )

        self.assertIn('- name: CLICKHOUSE_HOST\n          value: "ch.example.internal"', manifest)
        self.assertIn('- name: CLICKHOUSE_PORT\n          value: "9440"', manifest)
        self.assertIn('- name: CLICKHOUSE_DATABASE\n          value: "xmc"', manifest)
        self.assertIn('- name: CLICKHOUSE_USER\n          value: "reader"', manifest)

    def test_clickhouse_password_is_sourced_from_secret(self) -> None:
        """The password must come from a Secret, never an inline value or ConfigMap."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
            },
        )

        self.assertNotIn("s3cret", manifest)
        self.assertIn(
            "- name: CLICKHOUSE_PASSWORD\n"
            "          valueFrom:\n"
            "            secretKeyRef:\n"
            "              name: clickhouse-secret\n"
            "              key: clickhouse-password",
            manifest,
        )

    def test_clickhouse_secret_carries_configured_password(self) -> None:
        """The referenced Secret must exist and hold the configured password."""
        manifest = render_template(
            CLICKHOUSE_SECRET,
            {"metrics.enabled": "true", "metrics.clickhouse.password": "s3cret"},
        )

        self.assertIn("name: clickhouse-secret", manifest)
        self.assertIn('clickhouse-password: "s3cret"', manifest)

    def test_clickhouse_vars_fall_back_when_block_is_undefined(self) -> None:
        """An operator enabling metrics without a clickhouse block must still get a usable chart."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.enabled": "true",
                "metrics.clickhouse": "null",
            },
        )

        missing = REQUIRED_CLICKHOUSE_ENV_VARS - rendered_clickhouse_env_vars(manifest)

        self.assertEqual(missing, set(), f"fallback path dropped ClickHouse config: {missing}")
        self.assertIn(
            '- name: CLICKHOUSE_HOST\n          value: "clickhouse.xmc-metrics.svc.cluster.local"',
            manifest,
        )

    def test_clickhouse_secret_renders_when_block_is_undefined(self) -> None:
        """The Secret the deployment references must exist even with no clickhouse block."""
        manifest = render_template(
            CLICKHOUSE_SECRET, {"metrics.enabled": "true", "metrics.clickhouse": "null"}
        )

        self.assertIn("name: clickhouse-secret", manifest)
        self.assertIn('clickhouse-password: ""', manifest)

    def test_clickhouse_config_is_omitted_when_metrics_disabled(self) -> None:
        """ClickHouse is optional BYO infrastructure, so nothing renders when metrics are off."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT, {"dashboardServiceDeployment.enabled": "true"}
        )

        self.assertEqual(rendered_clickhouse_env_vars(manifest), set())

    def test_spanner_and_xid_config_remain_wired(self) -> None:
        """Guards the pre-existing ConfigMap contract read by cmd/dashboard_service/config.go."""
        manifest = render_template(
            DASHBOARD_CONFIGMAP, {"dashboardServiceDeployment.enabled": "true"}
        )

        self.assertIn("dashboard_spanner_database_name:", manifest)
        self.assertIn("dashboard_xid_service_addr:", manifest)


if __name__ == "__main__":
    unittest.main()
