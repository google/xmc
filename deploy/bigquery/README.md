# BigQuery Metrics Schema

[`schema.json`](schema.json) defines the canonical BigQuery `TableFieldSchema` for the `xmc_metrics` table used when XMC stores experiment metrics in BigQuery (instead of ClickHouse).

Column names, order, non-nullability (`REQUIRED`), and types mirror [`deploy/clickhouse/schema.sql`](../clickhouse/schema.sql) (`xmc_metrics`), enforced by [`schema_test.py`](schema_test.py).

---

## Partitioning & Clustering Contract

Always create `xmc_metrics` with:

- **Time Partitioning**: `DATE(timestamp)` (daily UTC partitions matching sanitized metric timestamps).
- **`require_partition_filter = false`**: Default dashboard queries filter on `WHERE xid = @xid AND metric_name = @metric_name` without an explicit timestamp range, relying on block pruning from the `xid, metric_name` cluster prefix.
- **Clustering**: `xid, metric_name, work_unit_id` (matching the dashboard query predicates so un-windowed experiment queries scan only blocks belonging to the target experiment and metric).

### Provisioning via `bq` CLI

```bash
bq mk --dataset --location=US "${PROJECT_ID}:xmc_metrics"

bq mk --table \
  --schema=deploy/bigquery/schema.json \
  --time_partitioning_field=timestamp \
  --time_partitioning_type=DAY \
  --require_partition_filter=false \
  --clustering_fields=xid,metric_name,work_unit_id \
  "${PROJECT_ID}:xmc_metrics.xmc_metrics"
```

### Equivalent BigQuery Standard SQL DDL

```sql
CREATE TABLE IF NOT EXISTS `xmc_metrics.xmc_metrics` (
  xid INT64 NOT NULL,
  work_unit_id INT64 NOT NULL,
  execution_id INT64 NOT NULL,
  job_name STRING NOT NULL,
  replica_id STRING NOT NULL,
  metric_name STRING NOT NULL,
  metric_value FLOAT64 NOT NULL,
  step INT64 NOT NULL,
  timestamp TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL
)
PARTITION BY DATE(timestamp)
CLUSTER BY xid, metric_name, work_unit_id
OPTIONS (
  require_partition_filter = false
);
```
