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

"""Helm template tests for the PostgreSQL schema migrations Job."""

import unittest

from chart_test_lib import render_template, require_helm

POSTGRES_MIGRATIONS_JOB_TEMPLATE = "templates/postgres/migrations-job.yaml"

POSTGRES_REQUIRED_VALUES: dict[str, str] = {
    "database.backend": "postgres",
    "database.experimentalPostgres": "true",
    "database.postgres.host": "pg.internal",
    "database.postgres.user": "xmc_user",
    "database.postgres.essDatabase": "ess_db",
    "database.postgres.xidDatabase": "xid_db",
}


class PostgresMigrationsJobChartTest(unittest.TestCase):
    """Verifies PostgreSQL schema migration Job rendering and opt-out."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_spanner_backend_renders_no_migrations_job(self) -> None:
        """Default Spanner backend renders no PostgreSQL migrations Job."""
        manifest = render_template(POSTGRES_MIGRATIONS_JOB_TEMPLATE, {})
        self.assertNotIn("kind:", manifest)

    def test_migrations_job_disabled_when_run_migrations_false(self) -> None:
        """Disabling database.postgres.runMigrations skips rendering the migration Job."""
        for raw_false in ("false", "0", "no"):
            with self.subTest(runMigrations=raw_false):
                values = {
                    **POSTGRES_REQUIRED_VALUES,
                    "database.postgres.runMigrations": raw_false,
                }
                manifest = render_template(POSTGRES_MIGRATIONS_JOB_TEMPLATE, values)
                self.assertNotIn("kind:", manifest)

    def test_migrations_job_renders_sequential_xid_and_ess_containers(self) -> None:
        """Migration Job renders sequential xid initContainer and ess container with hardening."""
        job = render_template(POSTGRES_MIGRATIONS_JOB_TEMPLATE, POSTGRES_REQUIRED_VALUES)
        self.assertIn("kind: Job", job)
        self.assertIn("name: postgres-migrations-r1\n", job)
        self.assertIn("ttlSecondsAfterFinished: 600", job)
        self.assertIn("backoffLimit: 2", job)
        self.assertIn("automountServiceAccountToken: false", job)
        self.assertIn("restartPolicy: Never", job)
        self.assertIn("readOnlyRootFilesystem: true", job)
        self.assertIn("runAsNonRoot: true", job)
        self.assertIn("runAsUser: 1000", job)
        self.assertIn("runAsGroup: 1000", job)
        self.assertIn("type: RuntimeDefault", job)
        self.assertIn("imagePullPolicy: Always", job)
        self.assertIn(
            'image: "us-docker.pkg.dev/YOUR_GCP_PROJECT_ID/xmc/postgres-migrator-dev:latest"',
            job,
        )
        self.assertIn("initContainers:\n      - name: migrate-xid", job)
        self.assertIn('"--migrations_dir=xid"', job)
        self.assertIn("containers:\n      - name: migrate-ess", job)
        self.assertIn('"--migrations_dir=ess"', job)
        self.assertIn('"--command=up"', job)
        self.assertIn('"--connect_timeout=90s"', job)
        self.assertIn("name: db-config", job)
        self.assertIn("name: postgres-secret", job)

    def test_migrations_job_honors_custom_image_override(self) -> None:
        """Overriding postgres_migrator_custom_image updates both init and main containers."""
        job = render_template(
            POSTGRES_MIGRATIONS_JOB_TEMPLATE,
            {
                **POSTGRES_REQUIRED_VALUES,
                "postgres_migrator_custom_image": "postgres_migrator_image:v2",
            },
        )
        self.assertEqual(job.count('image: "postgres_migrator_image:v2"'), 2)


if __name__ == "__main__":
    unittest.main()
