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

"""Helm template tests for optional in-cluster PostgreSQL deployment and BYO defaults."""

import re
import unittest

from chart_test_lib import render_template, require_helm

DB_CONFIG_TEMPLATE = "templates/db-config.yaml"
POSTGRES_SECRET_TEMPLATE = "templates/postgres-secret.yaml"
POSTGRES_SERVICE_TEMPLATE = "templates/postgres/service.yaml"
POSTGRES_INITDB_TEMPLATE = "templates/postgres/initdb-configmap.yaml"
POSTGRES_STATEFULSET_TEMPLATE = "templates/postgres/statefulset.yaml"

IN_CLUSTER_POSTGRES_VALUES: dict[str, str] = {
    "database.backend": "postgres",
    "database.experimentalPostgres": "true",
    "database.postgres.deploy": "true",
}

BYO_POSTGRES_VALUES: dict[str, str] = {
    "database.backend": "postgres",
    "database.experimentalPostgres": "true",
    "database.postgres.host": "pg.external.internal",
    "database.postgres.user": "byo_user",
    "database.postgres.essDatabase": "byo_ess",
    "database.postgres.xidDatabase": "byo_xid",
}


class PostgresDeployChartTest(unittest.TestCase):
    """Verifies in-cluster and BYO PostgreSQL manifest rendering and defaults."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_spanner_backend_renders_no_postgres_resources(self) -> None:
        """Default spanner backend renders no PostgreSQL StatefulSet, Service, ConfigMap, or Secret."""
        templates = (
            POSTGRES_SERVICE_TEMPLATE,
            POSTGRES_INITDB_TEMPLATE,
            POSTGRES_STATEFULSET_TEMPLATE,
            POSTGRES_SECRET_TEMPLATE,
        )
        for template in templates:
            with self.subTest(template=template):
                manifest = render_template(template, {})
                self.assertNotIn("kind:", manifest)

    def test_byo_postgres_renders_only_config_and_secret(self) -> None:
        """BYO mode (deploy: false) renders db-config and postgres-secret without K8s Postgres pods."""
        for template in (
            POSTGRES_SERVICE_TEMPLATE,
            POSTGRES_INITDB_TEMPLATE,
            POSTGRES_STATEFULSET_TEMPLATE,
        ):
            with self.subTest(template=template):
                self.assertNotIn("kind:", render_template(template, BYO_POSTGRES_VALUES))

        db_cm = render_template(DB_CONFIG_TEMPLATE, BYO_POSTGRES_VALUES)
        self.assertIn('POSTGRES_SSLMODE: "verify-full"', db_cm)
        secret = render_template(POSTGRES_SECRET_TEMPLATE, BYO_POSTGRES_VALUES)
        self.assertIn("name: postgres-secret", secret)

    def test_in_cluster_postgres_auto_defaults_db_config(self) -> None:
        """In-cluster mode auto-defaults host, port, user, databases, and sslMode=disable."""
        manifest = render_template(DB_CONFIG_TEMPLATE, IN_CLUSTER_POSTGRES_VALUES)
        svc = render_template(POSTGRES_SERVICE_TEMPLATE, IN_CLUSTER_POSTGRES_VALUES)
        ns_match = re.search(r"^\s*namespace:\s*(\S+)$", svc, re.MULTILINE)
        self.assertIsNotNone(ns_match)
        assert ns_match is not None

        self.assertIn('DB_BACKEND: "postgres"', manifest)
        self.assertIn(
            f'POSTGRES_HOST: "postgres.{ns_match.group(1)}.svc.cluster.local"',
            manifest,
        )
        self.assertIn('POSTGRES_PORT: "5432"', manifest)
        self.assertIn('POSTGRES_USER: "xmc"', manifest)
        self.assertIn('POSTGRES_ESS_DATABASE: "xmc_ess"', manifest)
        self.assertIn('POSTGRES_XID_DATABASE: "xmc_xid"', manifest)
        self.assertIn('POSTGRES_SSLMODE: "disable"', manifest)

    def test_in_cluster_postgres_respects_explicit_overrides(self) -> None:
        """Explicit connection and SSL overrides take precedence over in-cluster defaults."""
        values = {
            **IN_CLUSTER_POSTGRES_VALUES,
            "database.postgres.host": "custom-pg.xmc.svc.cluster.local",
            "database.postgres.port": "5433",
            "database.postgres.user": "custom_user",
            "database.postgres.essDatabase": "custom_ess",
            "database.postgres.xidDatabase": "custom_xid",
            "database.postgres.sslMode": "require",
        }
        manifest = render_template(DB_CONFIG_TEMPLATE, values)

        self.assertIn('POSTGRES_HOST: "custom-pg.xmc.svc.cluster.local"', manifest)
        self.assertIn('POSTGRES_PORT: "5433"', manifest)
        self.assertIn('POSTGRES_USER: "custom_user"', manifest)
        self.assertIn('POSTGRES_ESS_DATABASE: "custom_ess"', manifest)
        self.assertIn('POSTGRES_XID_DATABASE: "custom_xid"', manifest)
        self.assertIn('POSTGRES_SSLMODE: "require"', manifest)

    def test_in_cluster_postgres_renders_service_and_initdb_configmap(self) -> None:
        """In-cluster mode renders the ClusterIP Service and idempotent initdb script."""
        svc = render_template(POSTGRES_SERVICE_TEMPLATE, IN_CLUSTER_POSTGRES_VALUES)
        self.assertIn("kind: Service", svc)
        self.assertIn("name: postgres", svc)
        self.assertIn("name: tcp-postgres", svc)
        self.assertIn("port: 5432", svc)

        initdb = render_template(POSTGRES_INITDB_TEMPLATE, IN_CLUSTER_POSTGRES_VALUES)
        self.assertIn("kind: ConfigMap", initdb)
        self.assertIn("name: postgres-initdb", initdb)
        self.assertIn('for db in "xmc_ess" "xmc_xid"; do', initdb)

    def test_in_cluster_postgres_rejects_unsafe_identifiers(self) -> None:
        """Invalid SQL/shell identifiers for user or databases fail template rendering."""
        for key in ("user", "essDatabase", "xidDatabase"):
            with self.subTest(key=key):
                values = {
                    **IN_CLUSTER_POSTGRES_VALUES,
                    f"database.postgres.{key}": "bad-name;drop",
                }
                with self.assertRaises(RuntimeError) as raised:
                    render_template(POSTGRES_INITDB_TEMPLATE, values)
                self.assertIn(f"database.postgres.{key}", str(raised.exception))

    def test_in_cluster_statefulset_default_settings_and_empty_password(self) -> None:
        """Empty password uses POSTGRES_HOST_AUTH_METHOD=trust and omits POSTGRES_PASSWORD."""
        sts = render_template(POSTGRES_STATEFULSET_TEMPLATE, IN_CLUSTER_POSTGRES_VALUES)

        self.assertIn("kind: StatefulSet", sts)
        self.assertIn('image: "postgres:16-alpine"', sts)
        self.assertIn('value: "/var/lib/postgresql/data/pgdata"', sts)
        self.assertIn('name: POSTGRES_HOST_AUTH_METHOD\n          value: "trust"', sts)
        self.assertNotIn("POSTGRES_PASSWORD", sts)
        self.assertIn(
            "- pg_isready\n"
            "            - -U\n"
            '            - "xmc"\n'
            "            - -d\n"
            '            - "xmc_ess"',
            sts,
        )
        self.assertIn("defaultMode: 493", sts)
        self.assertIn('storage: "20Gi"', sts)
        self.assertNotIn("storageClassName", sts)

    def test_in_cluster_statefulset_with_password_and_storage_overrides(self) -> None:
        """Non-empty password wires secretKeyRef and omits POSTGRES_HOST_AUTH_METHOD=trust."""
        values = {
            **IN_CLUSTER_POSTGRES_VALUES,
            "database.postgres.password": "s3cret",
            "database.postgres.image": "postgres:16.4-alpine",
            "database.postgres.storageSize": "50Gi",
            "database.postgres.storageClassName": "premium-rwo",
        }
        sts = render_template(POSTGRES_STATEFULSET_TEMPLATE, values)

        self.assertIn('image: "postgres:16.4-alpine"', sts)
        self.assertIn('storage: "50Gi"', sts)
        self.assertIn('storageClassName: "premium-rwo"', sts)
        self.assertNotIn("POSTGRES_HOST_AUTH_METHOD", sts)
        self.assertIn(
            "- name: POSTGRES_PASSWORD\n"
            "          valueFrom:\n"
            "            secretKeyRef:\n"
            "              name: postgres-secret\n"
            "              key: postgres-password",
            sts,
        )


if __name__ == "__main__":
    unittest.main()
