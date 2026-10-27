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

from chart_test_lib import render_template, require_helm

DB_CONFIG_TEMPLATE = "templates/db-config.yaml"

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

    def test_legacy_service_configmap_keys_match_db_config(self) -> None:
        """Deprecated per-service ConfigMap keys render the same values as db-config."""
        scenarios = {
            "default": {
                "projectId": "my-project",
                "environment": "dev",
                "experimentStateServer.spannerDatabaseName": "",
                "xidService.spannerDatabaseName": "",
                "gcpServiceAccount": "sa@my-project.iam.gserviceaccount.com",
            },
            "overridden": {
                "projectId": "my-project",
                "environment": "dev",
                "experimentStateServer.spannerDatabaseName": "projects/p/instances/i/databases/ess-v1",
                "xidService.spannerDatabaseName": "projects/p/instances/i/databases/xid-v1",
                "gcpServiceAccount": "sa@my-project.iam.gserviceaccount.com",
            },
        }
        service_templates = (
            (
                "templates/ess/configmap.yaml",
                "ess_spanner_database_name",
                "SPANNER_ESS_DATABASE",
                "ess_quota_project_id",
            ),
            (
                "templates/xid/configmap.yaml",
                "xid_spanner_database_name",
                "SPANNER_XID_DATABASE",
                "xid_quota_project_id",
            ),
            (
                "templates/actuator/configmap.yaml",
                "spanner_database_name",
                "SPANNER_ESS_DATABASE",
                "quota_project_id",
            ),
            (
                "templates/reconciler/configmap.yaml",
                "spanner_database_name",
                "SPANNER_ESS_DATABASE",
                "quota_project_id",
            ),
        )

        for scenario_name, values in scenarios.items():
            db_manifest = render_template(DB_CONFIG_TEMPLATE, values)
            for template, legacy_db_key, db_config_key, legacy_quota_key in service_templates:
                with self.subTest(scenario=scenario_name, template=template):
                    svc_manifest = render_template(template, values)
                    expected_db = re.search(
                        rf"^\s*{db_config_key}: (.+)$", db_manifest, re.MULTILINE
                    )
                    expected_quota = re.search(
                        r"^\s*SPANNER_QUOTA_PROJECT_ID: (.+)$", db_manifest, re.MULTILINE
                    )
                    self.assertIsNotNone(expected_db)
                    self.assertIsNotNone(expected_quota)
                    self.assertIn(f"{legacy_db_key}: {expected_db.group(1)}", svc_manifest)
                    self.assertIn(f"{legacy_quota_key}: {expected_quota.group(1)}", svc_manifest)


if __name__ == "__main__":
    unittest.main()
