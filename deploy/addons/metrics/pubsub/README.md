# Pub/Sub Backend Setup Guide for the Ingestion Gateway

This directory documents the out-of-band Google Cloud setup required to run the XMC Ingestion Gateway ([`cmd/ingestion_gateway`](../../../../cmd/ingestion_gateway/README.md)) with `-message_bus=pubsub` instead of the default Redpanda backend.

Unlike the [Redpanda](../redpanda/README.md) and [ClickHouse](../clickhouse/README.md) addons, there is nothing to install into the cluster: Pub/Sub is a managed service. What this guide covers is the topic, the service accounts, and the IAM bindings the gateway needs, plus the Helm values that switch the backend on.

> [!IMPORTANT]
> Pub/Sub mode is a **producer-only** path. XMC has no consumer service of its own — ClickHouse consumes the `xmc-metrics` topic directly through its native `ENGINE = Kafka` table ([`deploy/clickhouse/schema.sql`](../../../clickhouse/schema.sql)), and ClickHouse has no Pub/Sub table engine. With `-message_bus=pubsub` the gateway publishes correctly, but nothing in XMC delivers those messages to ClickHouse. Choose this backend only if you are supplying your own subscriber.

---

## Prerequisites

1. **Google Cloud CLI (`gcloud`)**: Installed and authenticated (`gcloud auth login`).
2. **Target GKE cluster** with [Workload Identity](https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity) enabled, so the gateway pod can authenticate to Pub/Sub without a downloaded key.
3. **Project-level permission** to create Pub/Sub topics and manage IAM bindings (`roles/pubsub.admin` and `roles/iam.serviceAccountAdmin`, or equivalent).

Set the values used throughout this guide:

```bash
export PROJECT_ID="YOUR_GCP_PROJECT_ID"
export TOPIC_NAME="xmc-metrics"
export NAMESPACE="xmc"
export KSA_NAME="ingestion-gateway-service-account"
export GSA_NAME="xmc-ingestion-gateway"
export GSA_EMAIL="${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
```

---

## 1. Enable the Pub/Sub API and Create the Topic

```bash
gcloud services enable pubsub.googleapis.com --project "${PROJECT_ID}"

gcloud pubsub topics create "${TOPIC_NAME}" --project "${PROJECT_ID}"
```

To keep metric payloads inside a specific region, add a message storage policy:

```bash
gcloud pubsub topics create "${TOPIC_NAME}" \
    --project "${PROJECT_ID}" \
    --message-storage-policy-allowed-regions=us-central1
```

The gateway does **not** create the topic. It calls `Topic.Exists` at startup and refuses to start if the topic is missing, which is the Pub/Sub analogue of the Redpanda topology assertion (`-min_partitions` / `-min_replicas`).

If you manage infrastructure with Terraform, [`deploy/terraform/modules/log_monitor/main.tf`](../../../terraform/modules/log_monitor/main.tf) is the in-repo pattern for `google_pubsub_topic` plus `google_pubsub_topic_iam_member`. No Terraform module is shipped for the ingestion gateway topic.

---

## 2. Create the Google Service Account and Grant Publish Access

```bash
gcloud iam service-accounts create "${GSA_NAME}" \
    --project "${PROJECT_ID}" \
    --display-name="XMC Ingestion Gateway"
```

Grant two roles on the topic:

```bash
# Publish metric records.
gcloud pubsub topics add-iam-policy-binding "${TOPIC_NAME}" \
    --project "${PROJECT_ID}" \
    --member="serviceAccount:${GSA_EMAIL}" \
    --role="roles/pubsub.publisher"

# Read topic metadata for the startup existence check.
gcloud pubsub topics add-iam-policy-binding "${TOPIC_NAME}" \
    --project "${PROJECT_ID}" \
    --member="serviceAccount:${GSA_EMAIL}" \
    --role="roles/pubsub.viewer"
```

> [!WARNING]
> `roles/pubsub.publisher` grants only `pubsub.topics.publish`. It does **not** grant `pubsub.topics.get`, which the gateway's startup `Topic.Exists` check requires. Without the second binding the gateway fails at startup with a `PermissionDenied` error even though it would have been able to publish. Grant `roles/pubsub.viewer` as shown above, or a custom role containing `pubsub.topics.get`.

---

## 3. Bind the Kubernetes Service Account via Workload Identity

The chart creates the Kubernetes ServiceAccount and its `iam.gke.io/gcp-service-account` annotation (see step 4). Add the matching IAM binding on the Google side:

```bash
gcloud iam service-accounts add-iam-policy-binding "${GSA_EMAIL}" \
    --project "${PROJECT_ID}" \
    --role="roles/iam.workloadIdentityUser" \
    --member="serviceAccount:${PROJECT_ID}.svc.id.goog[${NAMESPACE}/${KSA_NAME}]"
```

---

## 4. Configure the Helm Chart

In [`deploy/helm/values.yaml`](../../../helm/values.yaml) (or your environment overlay):

```yaml
projectId: "YOUR_GCP_PROJECT_ID"

ingestionGatewayDeployment:
  enabled: true
  messageBus: "pubsub"
  topicName: "xmc-metrics"
  serviceAccountName: "ingestion-gateway-service-account"
  gcpServiceAccount: "xmc-ingestion-gateway@YOUR_GCP_PROJECT_ID.iam.gserviceaccount.com"
```

Notes on how these map onto gateway flags:

| Helm value | Gateway flag | Behaviour in `pubsub` mode |
| --- | --- | --- |
| `messageBus` | `-message_bus` | Selects the backend. Defaults to `redpanda`. |
| `topicName` | `-topic_name` | The Pub/Sub topic **ID** (not the full `projects/.../topics/...` path). |
| `projectId` (top level) | `-project_id` | The project that owns the topic. Required. |
| `serviceAccountName` | — | Name of the KSA the pod runs as. The chart creates it unless it is left as `default`. |
| `gcpServiceAccount` | — | Annotated onto the KSA for Workload Identity. Falls back to the top-level `gcpServiceAccount` when empty. |
| `redpandaBrokers`, `minPartitions`, `minReplicas`, `sasl`, `tls` | — | Not rendered. These are Kafka-only and the gateway rejects SASL/TLS in `pubsub` mode. |

Apply the chart as usual, then confirm the rendered arguments:

```bash
kubectl get deployment ingestion-gateway-deployment -n "${NAMESPACE}" \
    -o jsonpath='{.spec.template.spec.containers[0].args}'
```

---

## 5. Verification

### 1. Confirm the pod authenticated and passed the topic check

```bash
kubectl logs -n "${NAMESPACE}" -l app=ingestion-gateway --tail=50
```

A healthy startup logs the Pub/Sub project and topic. A missing topic or a missing `pubsub.topics.get` permission is a fatal startup error, not a runtime warning.

### 2. Publish a metric through the gateway

```bash
kubectl port-forward -n "${NAMESPACE}" svc/ingestion-gateway 8080:8080 &

curl -X POST http://localhost:8080/api/v1/metrics \
  -H "Content-Type: application/json" \
  -d '[{"xid":1001,"work_unit_id":1,"execution_id":1,"metric_name":"train_loss","metric_value":0.85,"step":10}]'
```

*Expected Response:*
```json
{"status":"accepted","count":1}
```

### 3. Pull the message back

Create a throwaway subscription with ordering enabled and read the record:

```bash
gcloud pubsub subscriptions create "${TOPIC_NAME}-verify" \
    --project "${PROJECT_ID}" \
    --topic "${TOPIC_NAME}" \
    --enable-message-ordering

gcloud pubsub subscriptions pull "${TOPIC_NAME}-verify" \
    --project "${PROJECT_ID}" \
    --auto-ack --limit=1 --format='value(message.orderingKey,message.data)'
```

The ordering key is the composite `xid:work_unit_id` — `1001:1` for the payload above. The data is a binary `Metric` protobuf, so it will not be human-readable.

Clean up the throwaway subscription when finished:

```bash
gcloud pubsub subscriptions delete "${TOPIC_NAME}-verify" --project "${PROJECT_ID}"
```

### 4. Confirm produce telemetry

```bash
kubectl port-forward -n "${NAMESPACE}" svc/ingestion-gateway 8080:8080 &
curl -s http://localhost:8080/metrics | grep xmc_ingestion_produce
```

`xmc_ingestion_produce_records_total` is recorded by both backends. The Kafka-only `franz_go_brokers_connections` series is absent in Pub/Sub mode.

---

## Operational Notes

- **Message ordering is always on.** The gateway sets `EnableMessageOrdering` and publishes with the `xid:work_unit_id` ordering key, matching the per-work-unit FIFO guarantee the Redpanda path gets from sticky-key partitioning. Any subscription you attach must also be created with `--enable-message-ordering`, or ordering is not preserved on delivery.
- **Per-key throughput ceiling.** Pub/Sub limits an ordering key to roughly 1 MB/s. A single very hot work unit will throttle before the topic does; the gateway surfaces this as HTTP 503.
- **Ordering keys suspend on failure.** Pub/Sub permanently stops publishing for an ordering key after any publish failure for that key. The gateway calls `ResumePublish` on every failed publish so a transient error cannot wedge a work unit for the process lifetime.
- **The network policy adapts automatically.** [`deploy/helm/templates/network-policies.yaml`](../../../helm/templates/network-policies.yaml) already permits TCP :443 egress, which covers `pubsub.googleapis.com`. In `pubsub` mode it drops the unused Kafka ports (9092/9094/9644) and instead allows TCP :80 to `169.254.169.254`, the node-local metadata server that Workload Identity fetches tokens from.
- **Switching back to Redpanda** is a values-only change: set `messageBus: "redpanda"` and restore `redpandaBrokers`. No data is migrated between backends.
