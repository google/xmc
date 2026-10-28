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

The dashboard service builds its chart data engine from the environment variables read by
pkg/clickhouse.GetOptions. A missing entry does not stop the pod from starting, so it stays
invisible until the deployment either fails readiness or answers GetChartData with an error.
These tests render the chart and assert the full set is wired.
"""

import re
import unittest

from chart_test_lib import render_template, require_helm

# Environment variables read by pkg/clickhouse.GetOptions. CLICKHOUSE_DSN is deliberately
# excluded: it is an alternative to these and the chart does not render it. The password is not
# listed here because its variable differs by secret backend; see PASSWORD_ENV_VAR_BY_BACKEND.
REQUIRED_CLICKHOUSE_ENV_VARS = frozenset(
    {
        "CLICKHOUSE_HOST",
        "CLICKHOUSE_PORT",
        "CLICKHOUSE_DATABASE",
        "CLICKHOUSE_USER",
    }
)

# The chart renders the password as a Secret reference for a plain release, and as a path into the
# Secret Manager CSI mount when the driver is enabled.
PASSWORD_ENV_VAR_BY_BACKEND = {False: "CLICKHOUSE_PASSWORD", True: "CLICKHOUSE_PASSWORD_FILE"}

DASHBOARD_DEPLOYMENT = "templates/dashboard-service/deployment.yaml"
DASHBOARD_CONFIGMAP = "templates/dashboard-service/configmap.yaml"
DASHBOARD_SERVICEACCOUNT = "templates/dashboard-service/serviceaccount.yaml"
CLICKHOUSE_SECRET = "templates/metrics/clickhouse-secret.yaml"
SECRET_PROVIDER_CLASS = "templates/ess/secretproviderclass.yaml"

ENV_VAR_NAME_PATTERN = re.compile(r"^\s*- name: (CLICKHOUSE_\w+)$", re.MULTILINE)


def rendered_clickhouse_env_vars(manifest: str) -> set[str]:
    """Returns the names of every CLICKHOUSE_* environment variable in a rendered manifest."""
    return set(ENV_VAR_NAME_PATTERN.findall(manifest))


def expected_clickhouse_env_vars(csi_enabled: bool = False) -> set[str]:
    """Returns the full variable set the dashboard service needs under the given secret backend."""
    return REQUIRED_CLICKHOUSE_ENV_VARS | {PASSWORD_ENV_VAR_BY_BACKEND[csi_enabled]}


class DashboardServiceClickhouseConfigTest(unittest.TestCase):
    """Asserts the dashboard service deployment receives complete ClickHouse configuration."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_all_required_clickhouse_vars_are_passed_when_metrics_enabled(self) -> None:
        """Every variable pkg/clickhouse reads must reach the container."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {"dashboardServiceDeployment.enabled": "true", "metrics.enabled": "true"},
        )

        missing = expected_clickhouse_env_vars() - rendered_clickhouse_env_vars(manifest)

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

    def test_clickhouse_password_is_read_from_csi_mount_when_enabled(self) -> None:
        """With the CSI driver on, the password must be a file path, never a value in the pod spec."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
                "secrets.csi.enabled": "true",
            },
        )

        missing = expected_clickhouse_env_vars(csi_enabled=True) - rendered_clickhouse_env_vars(
            manifest
        )

        self.assertEqual(missing, set(), f"CSI path is missing ClickHouse config: {missing}")
        self.assertNotIn("s3cret", manifest)
        self.assertNotIn("- name: CLICKHOUSE_PASSWORD\n", manifest)
        self.assertIn(
            '- name: CLICKHOUSE_PASSWORD_FILE\n          value: "/etc/gcp-secrets/clickhouse-password"',
            manifest,
        )

    def test_secret_provider_class_supplies_clickhouse_password(self) -> None:
        """The file the deployment reads must be one the provider class actually mounts."""
        manifest = render_template(
            SECRET_PROVIDER_CLASS,
            {
                "metrics.enabled": "true",
                "secrets.csi.enabled": "true",
                "secrets.csi.projectNumber": "123456789",
                "secrets.csi.clickhousePasswordSecretName": "xmc-clickhouse-password",
            },
        )

        self.assertIn(
            '- resourceName: "projects/123456789/secrets/xmc-clickhouse-password/versions/latest"',
            manifest,
        )
        self.assertIn('path: "clickhouse-password"', manifest)

    def test_secret_provider_class_omits_clickhouse_password_without_metrics(self) -> None:
        """A secret listed but absent from Secret Manager fails the mount for every service."""
        manifest = render_template(SECRET_PROVIDER_CLASS, {"secrets.csi.enabled": "true"})

        self.assertNotIn("clickhouse-password", manifest)

    def test_clickhouse_secret_is_not_rendered_when_csi_enabled(self) -> None:
        """The Helm-value copy of the password must disappear once Secret Manager supplies it."""
        manifest = render_template(
            CLICKHOUSE_SECRET,
            {
                "metrics.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
                "secrets.csi.enabled": "true",
            },
        )

        self.assertNotIn("kind: Secret", manifest)
        self.assertNotIn("s3cret", manifest)

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

        missing = expected_clickhouse_env_vars() - rendered_clickhouse_env_vars(manifest)

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

    def test_xid_and_component_config_remain_wired(self) -> None:
        """Guards the pre-existing ConfigMap contract read by cmd/dashboard_service/config.go."""
        manifest = render_template(
            DASHBOARD_CONFIGMAP, {"dashboardServiceDeployment.enabled": "true"}
        )

        # Deprecated: retained for one-release rollout compatibility until db-config images roll out.
        self.assertIn("dashboard_spanner_database_name:", manifest)
        self.assertIn("dashboard_xid_service_addr:", manifest)
        self.assertIn('enable_components: "false"', manifest)
        self.assertIn("components_namespace:", manifest)

    def test_configmap_wires_enable_components_when_enabled(self) -> None:
        """Enabling componentsDeployment propagates enable_components=true to dashboard-config."""
        manifest = render_template(
            DASHBOARD_CONFIGMAP,
            {"dashboardServiceDeployment.enabled": "true", "componentsDeployment.enabled": "true"},
        )
        self.assertIn('enable_components: "true"', manifest)

    def test_components_rbac_omitted_without_components(self) -> None:
        """The reader Role is only rendered when dashboard and components are both on."""
        for values in (
            {"dashboardServiceDeployment.enabled": "true", "componentsDeployment.enabled": "false"},
            {"dashboardServiceDeployment.enabled": "true"},
        ):
            self.assertNotIn(
                "dashboard-service-components-reader",
                render_template(DASHBOARD_SERVICEACCOUNT, values),
            )

    def test_components_rbac_rendered_when_components_enabled_even_without_metrics(self) -> None:
        """The reader Role renders when components are on, even if metrics.enabled is false."""
        manifest = render_template(
            DASHBOARD_SERVICEACCOUNT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "componentsDeployment.enabled": "true",
                "metrics.enabled": "false",
            },
        )
        self.assertIn("name: dashboard-service-components-reader", manifest)
        self.assertIn("name: dashboard-service-components-reader-binding", manifest)
        self.assertIn('resourceNames: ["clickhouse", "bigquery"]', manifest)
        self.assertIn('resourceNames: ["clickhouse-secret"]', manifest)

    def test_components_rbac_grants_bigquery_component_get(self) -> None:
        """The Role also covers the bigquery Component the data engine probes at startup.

        The dashboard counts a forbidden Component lookup as a configured store, so without this
        grant a release with neither ClickHouse nor BigQuery picks BigQuery and never turns ready.
        """
        manifest = render_template(
            DASHBOARD_SERVICEACCOUNT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "componentsDeployment.enabled": "true",
                "metrics.enabled": "false",
            },
        )
        self.assertIn('resourceNames: ["clickhouse", "bigquery"]', manifest)

    def test_components_rbac_grants_component_and_secret_get_when_csi_disabled(self) -> None:
        """Without CSI, the Role grants get on the clickhouse Component CR and password Secret."""
        manifest = render_template(
            DASHBOARD_SERVICEACCOUNT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "componentsDeployment.enabled": "true",
                "metrics.enabled": "true",
            },
        )
        self.assertIn("name: dashboard-service-components-reader", manifest)
        self.assertIn("name: dashboard-service-components-reader-binding", manifest)
        self.assertIn('resourceNames: ["clickhouse", "bigquery"]', manifest)
        self.assertIn('resourceNames: ["clickhouse-secret"]', manifest)
        self.assertIn(
            'resourceNames: ["custom-ch-secret"]',
            render_template(
                DASHBOARD_SERVICEACCOUNT,
                {
                    "dashboardServiceDeployment.enabled": "true",
                    "componentsDeployment.enabled": "true",
                    "metrics.enabled": "true",
                    "metrics.clickhouse.secretName": "custom-ch-secret",
                },
            ),
        )

    def test_components_rbac_omits_secret_rule_when_csi_enabled(self) -> None:
        """With CSI enabled, the password is mounted from Secret Manager so no Secret get is needed."""
        manifest = render_template(
            DASHBOARD_SERVICEACCOUNT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "componentsDeployment.enabled": "true",
                "metrics.enabled": "true",
                "secrets.csi.enabled": "true",
            },
        )
        self.assertIn("name: dashboard-service-components-reader", manifest)
        self.assertIn('resourceNames: ["clickhouse", "bigquery"]', manifest)
        self.assertNotIn('resources: ["secrets"]', manifest)


if __name__ == "__main__":
    unittest.main()
