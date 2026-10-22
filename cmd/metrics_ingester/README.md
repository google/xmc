# Metrics Ingester

The **Metrics Ingester** delivers metrics from Google Cloud Pub/Sub into ClickHouse. It is only needed when the [Ingestion Gateway](../ingestion_gateway/README.md) runs with `-message_bus=pubsub`. In Redpanda mode, ClickHouse consumes the topic itself through its `ENGINE = Kafka` table. ClickHouse has no Pub/Sub table engine, so this service fills that role.

> [!NOTE]
> There is no Helm chart or Terraform for this service yet, so for now the topic, subscription and IAM bindings must be created by hand.

---

## Data Flow

```mermaid
sequenceDiagram
    autonumber
    participant Gateway as Ingestion Gateway
    participant Topic as Pub/Sub topic (xmc-metrics)
    participant Ingester as Metrics Ingester
    participant Queue as xmc_metrics_pubsub_queue (Null)
    participant MV as xmc_metrics_pubsub_mv
    participant Table as xmc_metrics (MergeTree)

    Gateway->>Topic: Publish (protobuf Metric, ordering key "xid:work_unit_id")
    Topic-->>Ingester: Streaming pull on the subscription
    Ingester->>Queue: Batched INSERT
    Queue->>MV: Rows forwarded (nothing stored)
    MV->>Table: Filter, defaults, timestamp sanitization
    Ingester-->>Topic: Ack the batch
```

`xmc_metrics_pubsub_queue` has the same columns as the Kafka queue table and uses `ENGINE = Null`. Its view `xmc_metrics_pubsub_mv` runs the same `SELECT` as `xmc_metrics_mv`, so both buses reject the same records, apply the same defaults, and sanitize timestamps the same way. `deploy/clickhouse/schema_test.py` fails if the two views or the two queue tables drift apart.

---

## Delivery Semantics

- **At least once.** A batch is acked only after its INSERT succeeds. If the INSERT fails, every message in the batch is nacked for redelivery and the batch loop backs off exponentially (`250ms` to `30s`), so a record can be written twice after a partial failure or a crash. Configure a subscription `RetryPolicy` minimum backoff (for example `10s`–`600s`) as well so Pub/Sub spaces redeliveries across replicas during prolonged ClickHouse outages.
- **Batching.** A batch is flushed when it reaches `-batch_size` messages or when `-batch_interval` passes, whichever happens first. The defaults are 1000 messages and 1s. The interval matches the Kafka table's `kafka_flush_interval_ms`.
- **Malformed payloads** that fail protobuf wire unmarshaling as `cloud.xmanager.v1.Metric` are logged with a running drop count (`DroppedMessages`), acked, and dropped (unbounded, unlike `kafka_skip_broken_messages`, which caps skips at 100 per block). Because proto3 wire decoding is lenient, corrupt payloads that still form valid wire tags decode into near-empty messages and are filtered out by `xmc_metrics_pubsub_mv` (for example `xid = 0`).
- **Ordering** doesn't matter here. `xmc_metrics` is sorted by its `ORDER BY` key, so the subscription doesn't need message ordering enabled.
- **Flow control.** The client leases at most `2 × -batch_size` messages at a time.
- **Shutdown.** On SIGINT/SIGTERM, pending and already-buffered messages are flushed in `-batch_size` chunks within a shared 10s limit; any message arriving after the buffer drain (including during the flush) is nacked for redelivery. Total shutdown covers this 10s flush, Pub/Sub `Receive` teardown, and the 5s health server shutdown, so Kubernetes `terminationGracePeriodSeconds` must comfortably exceed ~15s (such as the 30s default).

---

## Configuration

Each setting is read from its flag first, then from its environment variable, then from the default.

| Flag | Environment | Default | Description |
| :--- | :--- | :--- | :--- |
| `-port` | `PORT` | `8080` | Port for the health endpoints. |
| `-project_id` | `GOOGLE_CLOUD_PROJECT` | *(required)* | GCP project that owns the subscription. |
| `-subscription_id` | `PUBSUB_SUBSCRIPTION_ID` | *(required)* | Subscription on the metrics topic. |
| `-batch_size` | — | `1000` | Maximum number of messages per INSERT. |
| `-batch_interval` | — | `1s` | Target time a received message waits before its batch is inserted; longer if a previous insert is still running. |

The ClickHouse connection comes from the shared `pkg/clickhouse` environment variables: `CLICKHOUSE_DSN`, or else `CLICKHOUSE_HOST`, `CLICKHOUSE_PORT`, `CLICKHOUSE_DATABASE`, `CLICKHOUSE_USER` and `CLICKHOUSE_PASSWORD` / `CLICKHOUSE_PASSWORD_FILE`.

### Authentication

The ingester uses Application Default Credentials. On GKE, use Workload Identity. The principal needs `roles/pubsub.subscriber` on the subscription, plus `pubsub.subscriptions.get` (for example through `roles/pubsub.viewer`) for the startup check. The subscription must already exist; the ingester never creates it and exits at startup if it's missing.

---

## Health Endpoints

| Path | Returns 200 when |
| :--- | :--- |
| `/healthz` | The receive loop is running. |
| `/readyz` | The receive loop is running and ClickHouse answers a ping. |

---

## Testing & Verification

```bash
# Unit tests. These use the in-memory Pub/Sub fake (pstest) and don't need network access.
bazel test //cmd/metrics_ingester/...

# Schema drift guard for the two materialized views
bazel test //deploy/clickhouse:schema_test

# End-to-end test against real ClickHouse in containers (manual; needs Docker)
bazel test //test/metrics:clickhouse_pipeline_test --test_filter=TestPubSubPipelineIngestion
```
