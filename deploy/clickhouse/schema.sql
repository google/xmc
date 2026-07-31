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
-- Matches Spanner Int64 ID types and exact 7-column sorting key order.
-- Partitioned by month (toYYYYMM(timestamp)) for efficient data retention and query pruning.
CREATE TABLE IF NOT EXISTS xmc_metrics
(
    xid Int64,
    work_unit_id Int64,
    execution_id Int64,
    replica_id String,
    metric_name String,
    metric_value Float64,
    step Int64,
    timestamp DateTime64(6, 'UTC'),
    created_at DateTime64(6, 'UTC') DEFAULT now64(6, 'UTC')
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (xid, metric_name, work_unit_id, execution_id, replica_id, step, timestamp);

-- 2. Virtual Queue Table reading native Protobuf binary records directly from Redpanda / Kafka.
-- Uses format 'ProtobufSingle' with schema 'metrics.proto:Metric' for single non-length-prefixed proto messages per Kafka record.
-- Note: 'metrics.proto' must be mounted at /var/lib/clickhouse/format_schemas/metrics.proto.
CREATE TABLE IF NOT EXISTS xmc_metrics_queue
(
    xid Int64,
    work_unit_id Int64,
    execution_id Int64,
    replica_id String,
    metric_name String,
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
    stream_flush_interval_ms = 1000;

-- 3. Materialized View to parse Protobuf fields, handle defaults via IF expressions,
-- convert microsecond epoch timestamps to DateTime64(6, 'UTC'), and populate xmc_metrics.
CREATE MATERIALIZED VIEW IF NOT EXISTS xmc_metrics_mv TO xmc_metrics AS
SELECT
    xid,
    work_unit_id,
    execution_id,
    if(replica_id = '', '0', replica_id) AS replica_id,
    metric_name,
    metric_value,
    step,
    if(timestamp = 0, now64(6, 'UTC'), fromUnixTimestamp64Micro(timestamp, 'UTC')) AS timestamp,
    now64(6, 'UTC') AS created_at
FROM xmc_metrics_queue
WHERE xid != 0 AND metric_name != '' AND execution_id != 0;
