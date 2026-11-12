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

"""Verifies Helm chart ClickHouse credential, CSI, and RBAC wiring for dashboard-service.

ClickHouse connection coordinates (host, port, database, user) are resolved dynamically at runtime
from Component/clickhouse by pkg/clickhouse/componentconfig. The umbrella chart owns only the
release-namespace password Secret (when CSI is disabled), the Secret Manager CSI mount +
CLICKHOUSE_PASSWORD_FILE wiring (when CSI is enabled with clickhousePasswordSecretName), and the
least-privilege Role granting dashboard-service access to Component CRs and the password Secret.
"""

import re
import unittest

from chart_test_lib import render_template, require_helm

DASHBOARD_DEPLOYMENT = "templates/dashboard-service/deployment.yaml"
DASHBOARD_CONFIGMAP = "templates/dashboard-service/configmap.yaml"
DASHBOARD_SERVICEACCOUNT = "templates/dashboard-service/serviceaccount.yaml"
CLICKHOUSE_SECRET = "templates/metrics/clickhouse-secret.yaml"
SECRET_PROVIDER_CLASS = "templates/ess/secretproviderclass.yaml"

ENV_VAR_NAME_PATTERN = re.compile(r"^\s*- name: (CLICKHOUSE_\w+)$", re.MULTILINE)


def rendered_clickhouse_env_vars(manifest: str) -> set[str]:
    """Returns the names of every CLICKHOUSE_* environment variable in a rendered manifest."""
    return set(ENV_VAR_NAME_PATTERN.findall(manifest))


class DashboardServiceClickhouseConfigTest(unittest.TestCase):
    """Asserts dashboard-service ClickHouse Secret, CSI, and Component RBAC chart contracts."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_no_static_clickhouse_env_vars_injected_without_csi(self) -> None:
        """ClickHouse coordinates and Secret are resolved via Component/clickhouse, not static env."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
            },
        )

        self.assertEqual(rendered_clickhouse_env_vars(manifest), set())
        self.assertNotIn("s3cret", manifest)

    def test_clickhouse_password_file_injected_when_csi_and_secret_name_configured(self) -> None:
        """With CSI and clickhousePasswordSecretName set, only CLICKHOUSE_PASSWORD_FILE is passed."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
                "secrets.csi.enabled": "true",
                "secrets.csi.clickhousePasswordSecretName": "xmc-clickhouse-password",
            },
        )

        self.assertEqual(rendered_clickhouse_env_vars(manifest), {"CLICKHOUSE_PASSWORD_FILE"})
        self.assertNotIn("s3cret", manifest)
        self.assertNotIn("- name: CLICKHOUSE_PASSWORD\n", manifest)
        self.assertIn(
            '- name: CLICKHOUSE_PASSWORD_FILE\n          value: "/etc/gcp-secrets/clickhouse-password"',
            manifest,
        )

    def test_clickhouse_password_file_omitted_when_csi_secret_name_empty(self) -> None:
        """CSI releases without a ClickHouse Secret Manager entry omit CLICKHOUSE_PASSWORD_FILE."""
        manifest = render_template(
            DASHBOARD_DEPLOYMENT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "secrets.csi.enabled": "true",
            },
        )

        self.assertEqual(rendered_clickhouse_env_vars(manifest), set())

    def test_clickhouse_csi_inactive_when_csi_disabled_or_secret_name_whitespace(self) -> None:
        """clickhouseUsesCsi requires both secrets.csi.enabled and a trimmed non-empty secret name."""
        cases = (
            (
                "csi_disabled_with_secret_name",
                {
                    "secrets.csi.enabled": "false",
                    "secrets.csi.clickhousePasswordSecretName": "xmc-ch",
                },
            ),
            (
                "csi_enabled_with_whitespace_secret_name",
                {"secrets.csi.enabled": "true", "secrets.csi.clickhousePasswordSecretName": "  "},
            ),
        )
        for name, csi_overrides in cases:
            with self.subTest(name):
                values = {
                    "dashboardServiceDeployment.enabled": "true",
                    "metrics.clickhouse.password": "s3cret",
                    **csi_overrides,
                }
                deployment = render_template(DASHBOARD_DEPLOYMENT, values)
                spc = render_template(SECRET_PROVIDER_CLASS, values)
                secret = render_template(CLICKHOUSE_SECRET, values)
                rbac = render_template(DASHBOARD_SERVICEACCOUNT, values)

                self.assertEqual(rendered_clickhouse_env_vars(deployment), set())
                self.assertNotIn("CLICKHOUSE_PASSWORD_FILE", deployment)
                self.assertNotIn("clickhouse-password", spc)
                self.assertIn("name: clickhouse-secret", secret)
                self.assertIn('clickhouse-password: "s3cret"', secret)
                self.assertIn('resources: ["secrets"]', rbac)
                self.assertIn('resourceNames: ["clickhouse-secret"]', rbac)

    def test_secret_provider_class_supplies_clickhouse_password(self) -> None:
        """The file the deployment reads must be one the provider class actually mounts."""
        manifest = render_template(
            SECRET_PROVIDER_CLASS,
            {
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

    def test_secret_provider_class_omits_clickhouse_password_by_default(self) -> None:
        """A secret listed but absent from Secret Manager fails the mount for every service."""
        manifest = render_template(SECRET_PROVIDER_CLASS, {"secrets.csi.enabled": "true"})

        self.assertNotIn("clickhouse-password", manifest)

    def test_clickhouse_secret_is_not_rendered_when_clickhouse_csi_active(self) -> None:
        """The Helm-value copy of the password must disappear once Secret Manager supplies it."""
        manifest = render_template(
            CLICKHOUSE_SECRET,
            {
                "dashboardServiceDeployment.enabled": "true",
                "metrics.clickhouse.password": "s3cret",
                "secrets.csi.enabled": "true",
                "secrets.csi.clickhousePasswordSecretName": "xmc-clickhouse-password",
            },
        )

        self.assertNotIn("kind: Secret", manifest)
        self.assertNotIn("s3cret", manifest)

    def test_csi_without_clickhouse_secret_name_preserves_k8s_secret_and_rbac_parity(self) -> None:
        """When CSI is on for JWT/OAuth but clickhousePasswordSecretName is empty, K8s Secret + RBAC stay active."""
        values = {
            "dashboardServiceDeployment.enabled": "true",
            "metrics.clickhouse.password": "s3cret",
            "secrets.csi.enabled": "true",
        }
        secret = render_template(CLICKHOUSE_SECRET, values)
        rbac = render_template(DASHBOARD_SERVICEACCOUNT, values)

        self.assertIn("name: clickhouse-secret", secret)
        self.assertIn('clickhouse-password: "s3cret"', secret)
        self.assertIn('resources: ["secrets"]', rbac)
        self.assertIn('resourceNames: ["clickhouse-secret"]', rbac)

    def test_clickhouse_secret_is_omitted_by_default_without_dashboard_or_password(self) -> None:
        """Default releases without dashboard-service or a password omit clickhouse-secret."""
        manifest = render_template(CLICKHOUSE_SECRET, {})

        self.assertNotIn("kind: Secret", manifest)

    def test_clickhouse_secret_carries_configured_password(self) -> None:
        """The referenced Secret must exist and hold the configured password."""
        manifest = render_template(
            CLICKHOUSE_SECRET,
            {"metrics.clickhouse.password": "s3cret"},
        )

        self.assertRegex(manifest, r"(?m)^apiVersion: v1$")
        self.assertIn("name: clickhouse-secret", manifest)
        self.assertIn('clickhouse-password: "s3cret"', manifest)

    def test_clickhouse_secret_renders_when_dashboard_enabled_and_block_undefined(self) -> None:
        """The Secret dashboard-service reads via K8s API must exist even with no clickhouse block."""
        manifest = render_template(
            CLICKHOUSE_SECRET,
            {"dashboardServiceDeployment.enabled": "true", "metrics.clickhouse": "null"},
        )

        self.assertIn("name: clickhouse-secret", manifest)
        self.assertIn('clickhouse-password: ""', manifest)

    def test_xid_and_component_config_remain_wired(self) -> None:
        """Guards the ConfigMap contract read by cmd/dashboard_service/config.go."""
        manifest = render_template(
            DASHBOARD_CONFIGMAP, {"dashboardServiceDeployment.enabled": "true"}
        )

        self.assertIn("dashboard_xid_service_addr:", manifest)
        self.assertIn("components_namespace:", manifest)
        self.assertNotIn("enable_components", manifest)

    def test_components_rbac_rendered_whenever_dashboard_enabled(self) -> None:
        """The reader Role renders whenever dashboard is enabled, and omits when disabled."""
        self.assertNotIn(
            "dashboard-service-components-reader",
            render_template(
                DASHBOARD_SERVICEACCOUNT, {"dashboardServiceDeployment.enabled": "false"}
            ),
        )
        for extra in ({}, {"componentsDeployment": "null"}):
            manifest = render_template(
                DASHBOARD_SERVICEACCOUNT,
                {"dashboardServiceDeployment.enabled": "true", **extra},
            )
            self.assertIn("name: dashboard-service-components-reader", manifest)
            self.assertIn("name: dashboard-service-components-reader-binding", manifest)
            self.assertIn(
                '  - apiGroups: ["xmc.dev"]\n'
                '    resources: ["components"]\n'
                '    resourceNames: ["clickhouse", "bigquery"]\n'
                '    verbs: ["get"]',
                manifest,
            )
            self.assertIn('resourceNames: ["clickhouse-secret"]', manifest)

    def test_actuator_clusterrole_unconditionally_grants_components_read(self) -> None:
        """Actuator ClusterRole always grants get/list/watch on xmc.dev components."""
        for values in ({}, {"componentsDeployment": "null"}):
            manifest = render_template("templates/actuator/clusterrole.yaml", values)
            self.assertIn(
                '- apiGroups: ["xmc.dev"]\n'
                '  resources: ["components"]\n'
                '  verbs: ["get", "list", "watch"]',
                manifest,
            )

    def test_null_and_scalar_overrides_do_not_panic_templates(self) -> None:
        """Guards against nil-map and scalar overrides on dashboardServiceDeployment and metrics."""
        secret_null_dash = render_template(
            CLICKHOUSE_SECRET,
            {"dashboardServiceDeployment": "null", "metrics.clickhouse.password": "s3cret"},
        )
        self.assertIn("name: clickhouse-secret", secret_null_dash)

        secret_scalar_metrics = render_template(
            CLICKHOUSE_SECRET,
            {"dashboardServiceDeployment.enabled": "true", "metrics": "true"},
        )
        self.assertIn("name: clickhouse-secret", secret_scalar_metrics)
        self.assertIn('clickhouse-password: ""', secret_scalar_metrics)

    def test_components_rbac_omits_secret_rule_when_clickhouse_csi_active(self) -> None:
        """With ClickHouse CSI active, the password is mounted from Secret Manager so no Secret get is needed."""
        manifest = render_template(
            DASHBOARD_SERVICEACCOUNT,
            {
                "dashboardServiceDeployment.enabled": "true",
                "secrets.csi.enabled": "true",
                "secrets.csi.clickhousePasswordSecretName": "xmc-clickhouse-password",
            },
        )
        self.assertIn("name: dashboard-service-components-reader", manifest)
        self.assertIn('resourceNames: ["clickhouse", "bigquery"]', manifest)
        self.assertNotIn('resources: ["secrets"]', manifest)

    def test_custom_clickhouse_secret_name_wired_across_secret_and_rbac(self) -> None:
        """Overriding metrics.clickhouse.secretName updates both the Secret and the reader Role."""
        values = {
            "dashboardServiceDeployment.enabled": "true",
            "metrics.clickhouse.secretName": "custom-ch-secret",
            "metrics.clickhouse.password": "s3cret",
        }
        secret = render_template(CLICKHOUSE_SECRET, values)
        rbac = render_template(DASHBOARD_SERVICEACCOUNT, values)

        self.assertIn("name: custom-ch-secret", secret)
        self.assertIn('resourceNames: ["custom-ch-secret"]', rbac)


if __name__ == "__main__":
    unittest.main()
