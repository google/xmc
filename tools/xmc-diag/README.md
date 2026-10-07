# Infrastructure Diagnostics CLI (`tools/xmc-diag`)

`xmc-diag` is a standalone Go command-line diagnostic tool for Platform Administrators and developers to perform out-of-band health checks, schema verifications, Kafka consumer group offset lag audits, and performance smoke tests against **Bring-Your-Own (BYO) ClickHouse** and **Redpanda / Kafka** clusters.

---

## 1. Overview & BYO Architecture

In Bring-Your-Own (BYO) infrastructure environments, ClickHouse database instances and Redpanda/Kafka message brokers are managed out-of-band by enterprise platform administrators. `xmc-diag` provides a zero-dependency diagnostic tool to verify connectivity, schema compliance, consumer offsets, and latency before or after deploying XMC services.

```mermaid
flowchart TD
    CLI["xmc-diag CLI"]
    
    subgraph ClickHouse Diagnostics
        CH_PING["Ping & Version Check"]
        CH_SCHEMA["Table Existence Check\n(xmc_metrics, xmc_metrics_queue, xmc_metrics_mv)"]
        CH_PERF["Query Smoke Test (SELECT 1)"]
    end

    subgraph Redpanda / Kafka Diagnostics
        RP_PING["Broker Connection & Metadata"]
        RP_TOPIC["Topic & Partition Metadata Audit"]
        RP_LAG["Consumer Group Offset & Lag Audit\n(xmc-clickhouse-ingester)"]
    end

    CLI --> CH_PING
    CLI --> CH_SCHEMA
    CLI --> CH_PERF
    CLI --> RP_PING
    CLI --> RP_TOPIC
    CLI --> RP_LAG
```

---

## 2. Key Features

- **ClickHouse Health & Schema Audit**: Pings the target database, retrieves server version, checks physical/queue/view table definitions (`xmc_metrics`, `xmc_metrics_queue`, `xmc_metrics_mv`), and evaluates query execution latency.
- **Redpanda / Kafka Metadata & Lag Audit**: Queries topic partition watermarks, reads consumer group committed offsets (`kadm.FetchOffsets` & `kadm.ListEndOffsets`), and calculates partition-level and total aggregate consumer lag.
- **Kubernetes `Component` CR Health Audit**: Queries `components.xmc.dev` custom resources in the target namespace (`xmc` by default) and verifies that all enabled components are reconciled (`STATE_READY`, `Ready=True`, matching `observedGeneration`, `Degraded!=True`) and all disabled components are `STATE_DISABLED`. Gracefully reports `Skipped: true` when the CRD or cluster is absent unless explicitly required via `--check-components=true`.
- **BYO Infrastructure Support**: Supports standard flags and environment overrides for custom host/port, DSN strings (`CLICKHOUSE_DSN`), SASL authentication (`SCRAM-SHA-256`, `SCRAM-SHA-512`, `PLAIN`), and TLS/mTLS parameters.
- **Flexible Output Formats**: Supports human-readable formatted text tables (default) or structured JSON (`--output=json`).

---

## 3. CLI Command-Line Flags

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-clickhouse_host` | `string` | `""` | ClickHouse host address (overrides `CLICKHOUSE_HOST` env). |
| `-clickhouse_port` | `int` | `9000` | ClickHouse native protocol port. |
| `-clickhouse_user` | `string` | `"default"` | ClickHouse database user. |
| `-clickhouse_password` | `string` | `""` | ClickHouse database password. |
| `-clickhouse_database` | `string` | `"default"` | ClickHouse target database name. |
| `-clickhouse_dsn` | `string` | `""` | Full ClickHouse DSN connection string (`clickhouse://user:pass@host:port/db`). |
| `-redpanda_brokers` | `string` | `"localhost:9092"` | Comma-separated list of Redpanda/Kafka broker addresses. |
| `-topic_name` | `string` | `"xmc-metrics"` | Metrics topic name in Redpanda/Kafka. |
| `-consumer_group` | `string` | `"xmc-clickhouse-ingester"` | Kafka consumer group name to audit offset lag for. |
| `-sasl_enabled` | `bool` | `false` | Enable SASL authentication for Redpanda connections. |
| `-sasl_mechanism` | `string` | `"SCRAM-SHA-256"` | SASL authentication mechanism (`SCRAM-SHA-256`, `SCRAM-SHA-512`, `PLAIN`). |
| `-sasl_user` | `string` | `""` | SASL authentication username. |
| `-sasl_password` | `string` | `""` | SASL authentication password. |
| `-tls_enabled` | `bool` | `false` | Enable TLS/mTLS for Redpanda connections. |
| `-tls_insecure_skip_verify` | `bool` | `false` | Skip TLS certificate verification (testing only). |
| `-check-components` / `-check_components` | `bool` | `true` | Audit `components.xmc.dev` CR readiness in Kubernetes (overrides `CHECK_COMPONENTS` / `ENABLE_COMPONENTS` env). |
| `-namespace` | `string` | `"xmc"` | Target Kubernetes namespace for `Component` CR checks (overrides `XMC_NAMESPACE` / `NAMESPACE` env). |
| `-kubeconfig` | `string` | `""` | Path to kubeconfig file for `Component` CR checks (overrides `KUBECONFIG` env). |
| `-output` | `string` | `"text"` | Output report format: `text` or `json`. |
| `-skip_smoke_tests` | `bool` | `false` | Skip latency performance smoke tests. |

---

## 4. Usage Examples

### Standard Health Check (Text Output)

```bash
xmc-diag \
  --clickhouse_host=clickhouse.example.com \
  --clickhouse_port=9000 \
  --redpanda_brokers=redpanda.example.com:9092 \
  --consumer_group=xmc-clickhouse-ingester
```

### JSON Output with BYO Authentication & DSN

```bash
xmc-diag \
  --clickhouse_dsn="clickhouse://ch_user:ch_pass@10.0.0.5:9000/xmc_db" \
  --redpanda_brokers="10.0.0.10:9092,10.0.0.11:9092" \
  --sasl_enabled=true \
  --sasl_user="redpanda_user" \
  --sasl_password="redpanda_password" \
  --output=json
```

### Inspecting Coordinates from the `clickhouse` `Component` CR

`xmc-diag` runs out-of-cluster and takes explicit flags rather than reading Kubernetes CRs directly. On clusters with `enable_components: true`, inspect the [`clickhouse` `Component` CR](../../deploy/profiles/stable-2026a/components/clickhouse/README.md), port-forward the target service, and pass the resolved coordinates via `-clickhouse_*` flags:

```bash
kubectl get component clickhouse -n xmc -o yaml
kubectl port-forward -n xmc-metrics svc/clickhouse 9000:9000 &
xmc-diag \
  --clickhouse_host=127.0.0.1 \
  --clickhouse_port=9000 \
  --clickhouse_database=default \
  --clickhouse_user=default \
  --clickhouse_password="$(kubectl get secret clickhouse-secret -n xmc -o jsonpath='{.data.clickhouse-password}' | base64 -d)"
```

---

## 5. Verification & Unit Testing

Run package unit tests via Go:

```bash
go test -v ./tools/xmc-diag/...
```

Run target build & unit tests via Bazel:

```bash
bazel test //tools/xmc-diag/...
```
