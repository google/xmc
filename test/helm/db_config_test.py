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

"""Helm template tests for the shared db-config ConfigMap and workload wiring."""

import re
import unittest

from chart_test_lib import render_chart, render_template, require_helm

DB_CONFIG_TEMPLATE = "templates/db-config.yaml"
POSTGRES_SECRET_TEMPLATE = "templates/postgres-secret.yaml"
HEALTH_DEPLOYMENT = "templates/health-service/deployment.yaml"

SERVICE_CONFIGMAPS: tuple[tuple[str, str], ...] = (
    ("templates/actuator/configmap.yaml", "actuator-config"),
    ("templates/dashboard-service/configmap.yaml", "dashboard-config"),
    ("templates/ess/configmap.yaml", "ess-config"),
    ("templates/health-service/configmap.yaml", "health-config"),
    ("templates/reconciler/configmap.yaml", "reconciler-config"),
    ("templates/xid/configmap.yaml", "xid-config"),
)

WORKLOAD_DEPLOYMENTS: tuple[str, ...] = (
    "templates/actuator/deployment.yaml",
    "templates/dashboard-service/deployment.yaml",
    "templates/ess/deployment.yaml",
    "templates/health-service/deployment.yaml",
    "templates/log-monitor/deployment.yaml",
    "templates/reconciler/deployment.yaml",
    "templates/rod/deployment.yaml",
    "templates/xid/deployment.yaml",
)

CHECKSUM_DEPLOYMENTS: tuple[str, ...] = (
    "templates/actuator/deployment.yaml",
    "templates/dashboard-service/deployment.yaml",
    "templates/ess/deployment.yaml",
    "templates/health-service/deployment.yaml",
    "templates/log-monitor/deployment.yaml",
    "templates/reconciler/deployment.yaml",
    "templates/rod/deployment.yaml",
    "templates/xid/deployment.yaml",
)

ENABLE_OPTIONAL_DEPLOYMENTS: dict[str, str] = {
    "dashboardServiceDeployment.enabled": "true",
    "logMonitorDeployment.enabled": "true",
    "rodDeployment.enabled": "true",
}

# The PostgreSQL opt-in plus the settings that have no default and must be set when the backend is
# postgres.
POSTGRES_REQUIRED_VALUES: dict[str, str] = {
    "database.backend": "postgres",
    "database.experimentalPostgres": "true",
    "database.postgres.host": "pg.internal",
    "database.postgres.user": "xmc_user",
    "database.postgres.essDatabase": "ess_db",
    "database.postgres.xidDatabase": "xid_db",
}


def db_config_checksum(manifest: str) -> str:
    """Returns the checksum/db-config pod annotation of a rendered Deployment."""
    match = re.search(r"^\s*checksum/db-config: (\S+)$", manifest, re.MULTILINE)
    if match is None:
        raise AssertionError("checksum/db-config not found in the rendered manifest")
    return match.group(1)


class DbConfigChartTest(unittest.TestCase):
    """Verifies db-config rendering for each backend and its wiring into the workloads."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_default_values_render_spanner_settings(self) -> None:
        """Without per-service database overrides, Spanner derives the environment database names."""
        manifest = render_template(
            DB_CONFIG_TEMPLATE,
            {
                "projectId": "my-project",
                "environment": "dev",
                "experimentStateServer.spannerDatabaseName": "",
            },
        )

        self.assertIn("kind: ConfigMap", manifest)
        self.assertIn("name: db-config", manifest)
        self.assertIn('DB_BACKEND: "spanner"', manifest)
        self.assertIn(
            'SPANNER_ESS_DATABASE: "projects/my-project/instances/xmc-dev/databases/experiment-db-dev"',
            manifest,
        )
        self.assertIn(
            'SPANNER_XID_DATABASE: "projects/my-project/instances/xmc-dev/databases/xids-db-dev"',
            manifest,
        )
        self.assertIn('SPANNER_QUOTA_PROJECT_ID: "my-project"', manifest)
        self.assertNotIn("POSTGRES_", manifest)

    def test_spanner_settings_follow_per_service_values(self) -> None:
        """The Spanner keys follow experimentStateServer and xidService spannerDatabaseName."""
        values = {
            "projectId": "my-project",
            "experimentStateServer.spannerDatabaseName": "projects/p/instances/i/databases/ess-v1",
            "xidService.spannerDatabaseName": "projects/p/instances/i/databases/xid-v1",
        }
        manifest = render_template(DB_CONFIG_TEMPLATE, values)

        self.assertIn('SPANNER_ESS_DATABASE: "projects/p/instances/i/databases/ess-v1"', manifest)
        self.assertIn('SPANNER_XID_DATABASE: "projects/p/instances/i/databases/xid-v1"', manifest)
        self.assertIn('SPANNER_QUOTA_PROJECT_ID: "my-project"', manifest)

    def test_postgres_backend_renders_only_postgres_settings(self) -> None:
        """database.backend: postgres renders the PostgreSQL keys instead of the Spanner keys."""
        values = {
            **POSTGRES_REQUIRED_VALUES,
            "database.postgres.port": "5433",
            "database.postgres.sslMode": "require",
        }
        manifest = render_template(DB_CONFIG_TEMPLATE, values)

        self.assertIn('DB_BACKEND: "postgres"', manifest)
        self.assertIn('POSTGRES_HOST: "pg.internal"', manifest)
        self.assertIn('POSTGRES_PORT: "5433"', manifest)
        self.assertIn('POSTGRES_USER: "xmc_user"', manifest)
        self.assertIn('POSTGRES_ESS_DATABASE: "ess_db"', manifest)
        self.assertIn('POSTGRES_XID_DATABASE: "xid_db"', manifest)
        self.assertIn('POSTGRES_SSLMODE: "require"', manifest)
        self.assertNotIn("SPANNER_", manifest)

    def test_postgres_backend_defaults_to_port_5432_and_verify_full(self) -> None:
        """Only the port and sslMode have defaults, and sslMode does not default to plaintext."""
        manifest = render_template(DB_CONFIG_TEMPLATE, POSTGRES_REQUIRED_VALUES)

        self.assertIn('POSTGRES_PORT: "5432"', manifest)
        self.assertIn('POSTGRES_SSLMODE: "verify-full"', manifest)

    def test_postgres_backend_without_connection_settings_fails_rendering(self) -> None:
        """database.backend: postgres fails the render unless host, user and databases are set."""
        for key in ("host", "user", "essDatabase", "xidDatabase"):
            with self.subTest(key=key):
                values = dict(POSTGRES_REQUIRED_VALUES)
                del values[f"database.postgres.{key}"]
                with self.assertRaises(RuntimeError) as raised:
                    render_template(DB_CONFIG_TEMPLATE, values)

                self.assertIn(f"database.postgres.{key} is required", str(raised.exception))

    def test_postgres_backend_without_opt_in_fails_rendering(self) -> None:
        """database.backend: postgres needs database.experimentalPostgres: true."""
        values = dict(POSTGRES_REQUIRED_VALUES)
        del values["database.experimentalPostgres"]
        with self.assertRaises(RuntimeError) as raised:
            render_template(DB_CONFIG_TEMPLATE, values)

        self.assertIn("set database.experimentalPostgres=true", str(raised.exception))

    def test_unknown_backend_fails_rendering(self) -> None:
        """An unsupported database.backend fails the render instead of defaulting to Spanner."""
        with self.assertRaises(RuntimeError) as raised:
            render_template(DB_CONFIG_TEMPLATE, {"database.backend": "mysql"})

        self.assertIn('database.backend must be "spanner" or "postgres"', str(raised.exception))

    def test_workloads_load_db_config(self) -> None:
        """Each workload that uses a database loads db-config as environment variables."""
        for template in WORKLOAD_DEPLOYMENTS:
            with self.subTest(template=template):
                manifest = render_template(template, ENABLE_OPTIONAL_DEPLOYMENTS)
                self.assertRegex(manifest, r"(?m)^\s*- configMapRef:\s*\n\s*name: db-config$")

    def test_workloads_restart_when_db_config_changes(self) -> None:
        """Each workload carries the db-config checksum, so a db-config change restarts it."""
        changed_values = {
            **ENABLE_OPTIONAL_DEPLOYMENTS,
            "xidService.spannerDatabaseName": "projects/p/instances/i/databases/xid-v2",
        }
        for template in CHECKSUM_DEPLOYMENTS:
            with self.subTest(template=template):
                default = db_config_checksum(render_template(template, ENABLE_OPTIONAL_DEPLOYMENTS))
                changed = db_config_checksum(render_template(template, changed_values))
                self.assertNotEqual(default, changed)

    def test_service_configmaps_exclude_legacy_db_keys(self) -> None:
        """Per-service ConfigMaps exclude Spanner DB keys; rod-data-provider-config is absent."""
        base_values = {
            **ENABLE_OPTIONAL_DEPLOYMENTS,
            "projectId": "my-project",
            "environment": "dev",
            "gcpServiceAccount": "sa@my-project.iam.gserviceaccount.com",
        }
        scenarios = {
            "default": {
                **base_values,
                "experimentStateServer.spannerDatabaseName": "",
                "xidService.spannerDatabaseName": "",
            },
            "overridden": {
                **base_values,
                "experimentStateServer.spannerDatabaseName": "projects/p/instances/i/databases/ess",
                "xidService.spannerDatabaseName": "projects/p/instances/i/databases/xid",
            },
        }
        for scenario, values in scenarios.items():
            for template, configmap_name in SERVICE_CONFIGMAPS:
                with self.subTest(scenario=scenario, configmap=configmap_name):
                    manifest = render_template(template, values)
                    self.assertIn(f"name: {configmap_name}", manifest)
                    self.assertNotIn("spanner_database_name", manifest)

        actuator_cm = render_template("templates/actuator/configmap.yaml", base_values)
        self.assertNotIn("quota_project_id", actuator_cm)
        self.assertIn(
            'ess_quota_project_id: "my-project"',
            render_template("templates/ess/configmap.yaml", base_values),
        )
        self.assertIn(
            'xid_quota_project_id: "my-project"',
            render_template("templates/xid/configmap.yaml", base_values),
        )
        self.assertIn(
            'quota_project_id: "my-project"',
            render_template("templates/reconciler/configmap.yaml", base_values),
        )
        self.assertNotIn("rod-data-provider-config", render_chart(base_values))
        self.assertNotIn(
            "rod-data-provider-config",
            render_template("templates/rod/deployment.yaml", base_values),
        )

    def test_health_service_mounts_db_config(self) -> None:
        """health-service mounts db-config under /config/db so the admin UI can list it."""
        manifest = render_template(HEALTH_DEPLOYMENT, {})

        self.assertIn(
            "- name: db-config-volume\n          configMap:\n            name: db-config", manifest
        )
        self.assertIn("- name: db-config-volume\n            mountPath: /config/db", manifest)

    def test_log_monitor_deployment_enabled_by_default(self) -> None:
        """log-monitor deployment and health-config GKE URL render with default values."""
        lm_manifest = render_template("templates/log-monitor/deployment.yaml", {})
        self.assertIn("name: log-monitor-deployment", lm_manifest)
        self.assertNotIn("-spanner_database_name", lm_manifest)
        self.assertNotIn("-quota_project_id", lm_manifest)

        health_cm = render_template("templates/health-service/configmap.yaml", {})
        self.assertIn('health_log_monitor_url: "http://log-monitor-service:8080"', health_cm)
        self.assertNotIn("health_log_monitor_resource_name", health_cm)

        health_cm_cloud_run = render_template(
            "templates/health-service/configmap.yaml",
            {"logMonitorDeployment.enabled": "false"},
        )
        self.assertIn("health_log_monitor_resource_name:", health_cm_cloud_run)
        self.assertNotIn("health_log_monitor_url", health_cm_cloud_run)

    def test_workloads_source_postgres_password_from_secret(self) -> None:
        """Every workload receives POSTGRES_PASSWORD from postgres-secret when database.backend is postgres."""
        values = {
            **ENABLE_OPTIONAL_DEPLOYMENTS,
            **POSTGRES_REQUIRED_VALUES,
            "database.postgres.password": "s3cret",
        }
        for template in WORKLOAD_DEPLOYMENTS:
            with self.subTest(template=template):
                manifest = render_template(template, values)
                self.assertNotIn("s3cret", manifest)
                self.assertIn(
                    "- name: POSTGRES_PASSWORD\n"
                    "          valueFrom:\n"
                    "            secretKeyRef:\n"
                    "              name: postgres-secret\n"
                    "              key: postgres-password",
                    manifest,
                )

    def test_workloads_omit_postgres_password_when_spanner_backend(self) -> None:
        """Spanner deployments do not inject POSTGRES_PASSWORD."""
        for template in WORKLOAD_DEPLOYMENTS:
            with self.subTest(template=template):
                manifest = render_template(template, ENABLE_OPTIONAL_DEPLOYMENTS)
                self.assertNotIn("POSTGRES_PASSWORD", manifest)

    def test_postgres_secret_carries_configured_password(self) -> None:
        """postgres-secret renders with the configured password when database.backend is postgres."""
        manifest = render_template(
            POSTGRES_SECRET_TEMPLATE,
            {
                **POSTGRES_REQUIRED_VALUES,
                "database.postgres.password": "s3cret",
            },
        )
        self.assertIn("name: postgres-secret", manifest)
        self.assertIn('postgres-password: "s3cret"', manifest)

    def test_postgres_secret_is_not_rendered_when_spanner_backend(self) -> None:
        """postgres-secret is omitted when backend is Spanner."""
        manifest = render_template(
            POSTGRES_SECRET_TEMPLATE, {"database.postgres.password": "s3cret"}
        )
        self.assertNotIn("kind: Secret", manifest)
        self.assertNotIn("s3cret", manifest)


if __name__ == "__main__":
    unittest.main()
