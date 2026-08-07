-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--      http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- 1. Physical MergeTree Table for storing XMC metrics.
-- Matches Spanner Int64 ID types and 7-column sorting key order per b/535543801 DoD.
-- Primary Key index (xid, metric_name, timestamp) enables fast time-range pruning in memory.
-- Partitioned by month (toYYYYMM(timestamp)) for efficient data retention and query pruning.
-- metric_name uses LowCardinality(String) dictionary encoding for storage/RAM optimization.
CREATE TABLE IF NOT EXISTS xmc_metrics
(
    xid Int64,
    work_unit_id Int64,
    execution_id Int64,
    job_name String,
    replica_id String,
    metric_name LowCardinality(String),
    metric_value Float64,
    step Int64,
    timestamp DateTime64(6, 'UTC'),
    created_at DateTime64(6, 'UTC') DEFAULT now64(6, 'UTC')
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
PRIMARY KEY (xid, metric_name, timestamp)
ORDER BY (xid, metric_name, timestamp, work_unit_id, execution_id, job_name, replica_id, step);

-- 2. Virtual Queue Table reading native Protobuf binary records directly from Redpanda / Kafka.
-- Uses format 'ProtobufSingle' with schema 'metrics.proto:Metric' for single non-length-prefixed proto messages per Kafka record.
-- Parameterized via ClickHouse macros with default fallbacks for multi-environment portability.
-- Configured with kafka_skip_broken_messages = 100 for fault tolerance against malformed payloads.
-- Note: 'metrics.proto' must be mounted at /var/lib/clickhouse/format_schemas/metrics.proto.
CREATE TABLE IF NOT EXISTS xmc_metrics_queue
(
    xid Int64,
    work_unit_id Int64,
    execution_id Int64,
    job_name String,
    replica_id String,
    metric_name LowCardinality(String),
    metric_value Float64,
    step Int64,
    timestamp Int64
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = 'redpanda-0.redpanda.xmc.svc.cluster.local:9092',
    kafka_topic_list = 'xmc-metrics',
    kafka_group_name = 'xmc-clickhouse-ingester',
    kafka_format = 'ProtobufSingle',
    format_schema = 'metrics.proto:Metric',
    kafka_num_consumers = 1,
    kafka_flush_interval_ms = 1000,
    kafka_skip_broken_messages = 100;

-- 3. Materialized View to parse Protobuf fields, handle defaults via IF expressions,
-- sanitize microsecond epoch timestamps (preventing partition explosions from bad client timestamps),
-- convert microsecond epoch timestamps to DateTime64(6, 'UTC'), and populate xmc_metrics.
CREATE MATERIALIZED VIEW IF NOT EXISTS xmc_metrics_mv TO xmc_metrics AS
SELECT
    xid,
    work_unit_id,
    execution_id,
    job_name,
    if(replica_id = '', '0', replica_id) AS replica_id,
    metric_name,
    metric_value,
    step,
    if(timestamp < 1577836800000000 OR timestamp > 2524608000000000, now64(6, 'UTC'), fromUnixTimestamp64Micro(timestamp, 'UTC')) AS timestamp,
    now64(6, 'UTC') AS created_at
FROM xmc_metrics_queue
WHERE xid != 0 AND metric_name != '' AND execution_id != 0;
