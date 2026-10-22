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

import unittest

from chart_test_lib import render_template, require_helm

DB_CONFIG_TEMPLATE = "templates/db-config.yaml"
ESS_CONFIGMAP = "templates/ess/configmap.yaml"

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

ENABLE_OPTIONAL_DEPLOYMENTS: dict[str, str] = {
    "dashboardServiceDeployment.enabled": "true",
    "logMonitorDeployment.enabled": "true",
    "rodDeployment.enabled": "true",
}


class DbConfigChartTest(unittest.TestCase):
    """Verifies db-config rendering for each backend and its wiring into the workloads."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_default_values_render_spanner_settings(self) -> None:
        """Default values select Spanner and render only the Spanner keys."""
        manifest = render_template(DB_CONFIG_TEMPLATE, {})

        self.assertIn("kind: ConfigMap", manifest)
        self.assertIn("name: db-config", manifest)
        self.assertIn('DB_BACKEND: "spanner"', manifest)
        self.assertIn(
            'SPANNER_ESS_DATABASE: "projects/YOUR_GCP_PROJECT_ID/instances/'
            'YOUR_SPANNER_INSTANCE/databases/YOUR_SPANNER_DATABASE"',
            manifest,
        )
        self.assertIn(
            'SPANNER_XID_DATABASE: "projects/YOUR_GCP_PROJECT_ID/instances/'
            'xmc-YOUR_ENVIRONMENT_NAME/databases/xids-db-YOUR_ENVIRONMENT_NAME"',
            manifest,
        )
        self.assertIn('SPANNER_QUOTA_PROJECT_ID: "YOUR_GCP_PROJECT_ID"', manifest)
        self.assertNotIn("POSTGRES_", manifest)

    def test_spanner_settings_follow_per_service_values(self) -> None:
        """The Spanner keys use the values the per-service ConfigMaps already use."""
        values = {
            "projectId": "my-project",
            "experimentStateServer.spannerDatabaseName": "projects/p/instances/i/databases/ess-v1",
            "xidService.spannerDatabaseName": "projects/p/instances/i/databases/xid-v1",
        }
        manifest = render_template(DB_CONFIG_TEMPLATE, values)

        self.assertIn('SPANNER_ESS_DATABASE: "projects/p/instances/i/databases/ess-v1"', manifest)
        self.assertIn('SPANNER_XID_DATABASE: "projects/p/instances/i/databases/xid-v1"', manifest)
        self.assertIn('SPANNER_QUOTA_PROJECT_ID: "my-project"', manifest)
        self.assertIn(
            'ess_spanner_database_name: "projects/p/instances/i/databases/ess-v1"',
            render_template(ESS_CONFIGMAP, values),
        )

    def test_postgres_backend_renders_only_postgres_settings(self) -> None:
        """database.backend: postgres renders the PostgreSQL keys instead of the Spanner keys."""
        values = {
            "database.backend": "postgres",
            "database.postgres.host": "pg.internal",
            "database.postgres.port": "5433",
            "database.postgres.user": "xmc_user",
            "database.postgres.essDatabase": "ess_db",
            "database.postgres.xidDatabase": "xid_db",
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
                self.assertIn("- configMapRef:\n            name: db-config", manifest)


if __name__ == "__main__":
    unittest.main()
