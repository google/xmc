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

"""Consistency tests for deploy/clickhouse/schema.sql.

The Redpanda and Pub/Sub message buses feed xmc_metrics through separate materialized views that
must apply the same filtering, defaults and timestamp sanitization. These tests fail when the two
definitions drift apart.
"""

import pathlib
import re
import unittest

_SCHEMA_PATH = pathlib.Path(__file__).parent / "schema.sql"


def _strip_comments(sql: str) -> str:
    """Returns sql with full-line `--` comments removed."""
    return "\n".join(line for line in sql.splitlines() if not line.lstrip().startswith("--"))


def _view_select(sql: str, view_name: str) -> str:
    """Returns the SELECT body of the named materialized view, whitespace-normalized."""
    pattern = (
        rf"CREATE MATERIALIZED VIEW IF NOT EXISTS {view_name} TO xmc_metrics AS\s+"
        r"(SELECT.*?);"
    )
    match = re.search(pattern, sql, re.DOTALL)
    if match is None:
        raise AssertionError(f"materialized view {view_name} not found in schema.sql")
    return " ".join(match.group(1).split())


def _table_columns(sql: str, table_name: str) -> str:
    """Returns the column list of the named table, whitespace-normalized."""
    pattern = rf"CREATE TABLE IF NOT EXISTS {table_name}\s*\((.*?)\)\s*ENGINE"
    match = re.search(pattern, sql, re.DOTALL)
    if match is None:
        raise AssertionError(f"table {table_name} not found in schema.sql")
    return " ".join(match.group(1).split())


class SchemaConsistencyTest(unittest.TestCase):
    def setUp(self):
        self.sql = _strip_comments(_SCHEMA_PATH.read_text())

    def test_pubsub_view_matches_kafka_view(self):
        kafka_select = _view_select(self.sql, "xmc_metrics_mv")
        pubsub_select = _view_select(self.sql, "xmc_metrics_pubsub_mv")

        self.assertIn("FROM xmc_metrics_queue ", kafka_select)
        self.assertIn("FROM xmc_metrics_pubsub_queue ", pubsub_select)
        self.assertEqual(
            kafka_select.replace("FROM xmc_metrics_queue ", "FROM <source> "),
            pubsub_select.replace("FROM xmc_metrics_pubsub_queue ", "FROM <source> "),
        )

    def test_pubsub_queue_columns_match_kafka_queue(self):
        self.assertEqual(
            _table_columns(self.sql, "xmc_metrics_queue"),
            _table_columns(self.sql, "xmc_metrics_pubsub_queue"),
        )

    def test_view_select_rejects_missing_view(self):
        with self.assertRaisesRegex(AssertionError, "not found"):
            _view_select(self.sql, "no_such_view")

    def test_metrics_addons_manifest_mounts_init_scripts_and_redpanda_poststart(self):
        addons_dir = _SCHEMA_PATH.parent.parent / "addons" / "metrics"
        manifest = (addons_dir / "manifests" / "metrics-addons.yaml").read_text()
        setup_script = (addons_dir / "setup_metrics_addons.sh").read_text()

        self.assertIn("CLICKHOUSE_ALWAYS_RUN_INITDB_SCRIPTS", manifest)
        self.assertIn("/docker-entrypoint-initdb.d/init.sql", manifest)
        self.assertIn("name: clickhouse-init-scripts", manifest)
        self.assertIn("rpk topic create xmc-metrics --partitions 128 --replicas 1", manifest)
        self.assertIn("publishNotReadyAddresses: true", manifest)
        self.assertIn("kubectl create configmap clickhouse-init-scripts", setup_script)


if __name__ == "__main__":
    unittest.main()
