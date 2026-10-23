# Metrics Ingester

The **Metrics Ingester** delivers metrics from Google Cloud Pub/Sub into ClickHouse when the [Ingestion Gateway](../ingestion_gateway/README.md) runs with `-message_bus=pubsub` (in Redpanda mode, ClickHouse consumes the topic directly via `ENGINE = Kafka`). Running it as a separate service keeps ClickHouse credentials and egress off workload-reachable gateway pods and lets a small consumer replica count batch large inserts independently of gateway HTTP autoscaling.

> [!NOTE]
> Helm and Terraform wiring are not in place yet; create the Pub/Sub topic, subscription, and IAM bindings (`roles/pubsub.subscriber` via Workload Identity / ADC) manually.

## Data Flow & Delivery Semantics

The ingester pulls `cloud.xmanager.v1.Metric` protobuf messages from the subscription (leasing at most `2 × -batch_size`) and batch-inserts into the `ENGINE = Null` table `xmc_metrics_pubsub_queue` in [schema.sql](../../deploy/clickhouse/schema.sql), whose materialized view `xmc_metrics_pubsub_mv` applies the same filtering, defaults, and timestamp sanitization as `xmc_metrics_mv` before writing `xmc_metrics`. Subscription message ordering is not required because `xmc_metrics` sorts by its `ORDER BY` key.

- **At-least-once & backoff**: Batches flush at `-batch_size` (`1000`) or `-batch_interval` (`1s`) and ack only after `INSERT` succeeds. Failed writes nack the batch and back off exponentially (`250ms`–`30s`); configure a subscription `RetryPolicy` minimum backoff (e.g. `10s`–`600s`) as well to space redeliveries across replicas.
- **Malformed payloads**: Payloads failing protobuf wire unmarshaling are logged with `dropped_total`, acked, and dropped (unbounded, unlike `kafka_skip_broken_messages`); valid wire tags that decode near-empty are filtered by `xmc_metrics_pubsub_mv` (`xid = 0`).
- **Shutdown**: In-flight writes finish within `10s`; un-flushed pending messages are nacked on `SIGINT`/`SIGTERM` for redelivery.

## Configuration & Testing

| Flag | Environment Default | Built-in Default | Description |
| :--- | :--- | :--- | :--- |
| `-project_id` | `GOOGLE_CLOUD_PROJECT` | *(required)* | GCP project owning the subscription. |
| `-subscription_id` | `PUBSUB_SUBSCRIPTION_ID` | *(required)* | Subscription ID on the metrics topic. |
| `-batch_size` | — | `1000` | Maximum messages per ClickHouse `INSERT`. |
| `-batch_interval` | — | `1s` | Target wait before flushing a partial batch (longer if a prior insert is running). |

ClickHouse connection settings come from `pkg/clickhouse`: `CLICKHOUSE_DSN`, or `CLICKHOUSE_HOST`, `CLICKHOUSE_PORT`, `CLICKHOUSE_DATABASE`, `CLICKHOUSE_USER`, and `CLICKHOUSE_PASSWORD` / `CLICKHOUSE_PASSWORD_FILE`.

```bash
bazel test //cmd/metrics_ingester/...
```
