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

"""Schema parity tests between deploy/bigquery/schema.json and deploy/clickhouse/schema.sql.

The BigQuery and ClickHouse metrics backends expose the same logical xmc_metrics table to
cmd/metrics_ingester and cmd/dashboard_service. These tests fail if column order, names,
nullability, or logical types drift apart.
"""

import json
import pathlib
import re
import unittest

_BASE_DIR = pathlib.Path(__file__).parent
_BQ_SCHEMA_PATH = _BASE_DIR / "schema.json"
_CH_SCHEMA_PATH = _BASE_DIR.parent / "clickhouse" / "schema.sql"

_CH_TO_BQ_TYPES: dict[str, str] = {
    "Int64": "INT64",
    "String": "STRING",
    "LowCardinality(String)": "STRING",
    "Float64": "FLOAT64",
    "DateTime64(6, 'UTC')": "TIMESTAMP",
}


def _strip_sql_comments(sql: str) -> str:
    """Returns sql with full-line `--` comments removed."""
    return "\n".join(line for line in sql.splitlines() if not line.lstrip().startswith("--"))


def _map_clickhouse_type(ch_type: str) -> str:
    """Maps a ClickHouse column type declaration from xmc_metrics to its BigQuery equivalent."""
    normalized = " ".join(ch_type.split())
    for prefix, bq_type in _CH_TO_BQ_TYPES.items():
        if normalized == prefix or normalized.startswith(f"{prefix} DEFAULT "):
            return bq_type
    raise ValueError(f"unsupported ClickHouse column type: {ch_type!r}")


def _clickhouse_table_columns(sql: str, table_name: str) -> list[tuple[str, str]]:
    """Extracts ordered (column_name, mapped_bigquery_type) pairs from a ClickHouse table DDL."""
    pattern = rf"CREATE TABLE IF NOT EXISTS {table_name}\s*\((.*?)\)\s*ENGINE"
    match = re.search(pattern, _strip_sql_comments(sql), re.DOTALL)
    if match is None:
        raise AssertionError(f"table {table_name} not found in schema.sql")

    columns: list[tuple[str, str]] = []
    for raw_col in match.group(1).split(",\n"):
        cleaned = raw_col.strip().rstrip(",")
        if not cleaned:
            continue
        col_name, col_type = cleaned.split(None, 1)
        columns.append((col_name, _map_clickhouse_type(col_type)))
    return columns


class BigQuerySchemaParityTest(unittest.TestCase):
    """Verifies 1-to-1 column and type parity between BigQuery and ClickHouse schemas."""

    def setUp(self) -> None:
        self.bq_fields: list[dict[str, str]] = json.loads(_BQ_SCHEMA_PATH.read_text())
        self.ch_sql: str = _CH_SCHEMA_PATH.read_text()

    def test_bigquery_columns_match_clickhouse_xmc_metrics(self) -> None:
        """Asserts ordered column names and mapped types match ClickHouse xmc_metrics."""
        ch_columns = _clickhouse_table_columns(self.ch_sql, "xmc_metrics")
        bq_columns = [(field["name"], field["type"]) for field in self.bq_fields]
        self.assertEqual(ch_columns, bq_columns)

    def test_all_bigquery_fields_are_required_and_documented(self) -> None:
        """Asserts every BigQuery field is non-nullable (REQUIRED) with a description."""
        for field in self.bq_fields:
            self.assertEqual(field.get("mode"), "REQUIRED", f"field {field['name']}")
            self.assertTrue(field.get("description", "").strip(), f"field {field['name']}")

    def test_parser_rejects_unknown_table_and_type(self) -> None:
        """Asserts helper parsers fail fast on missing tables or unmapped ClickHouse types."""
        with self.assertRaisesRegex(AssertionError, "not found"):
            _clickhouse_table_columns(self.ch_sql, "missing_table")
        with self.assertRaisesRegex(ValueError, "unsupported ClickHouse column type"):
            _map_clickhouse_type("Nullable(Int64)")


if __name__ == "__main__":
    unittest.main()
