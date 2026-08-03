# Redpanda Addon Deployment Guide for GKE

This directory contains the documentation and example Helm configuration (`values.yaml`) for deploying **Redpanda** onto a Google Kubernetes Engine (GKE) cluster.

Redpanda serves as the high-throughput, Kafka-compatible event streaming message broker backend for XManager on Cloud (XMC) metrics ingestion and testing using `cmd/ingestion_gateway`.

---

## Prerequisites

1. **Google Cloud CLI (`gcloud`)**: Installed and authenticated (`gcloud auth login`).
2. **Kubernetes CLI (`kubectl`)**: Installed and configured to access your target GKE cluster.
3. **Helm 3 (`helm`)**: Installed on your workstation.
4. **Target GKE Cluster**: An active GKE cluster with a nodepool named `main-pool` (or modify `nodeSelector` in `values.yaml` to match your target nodepool).

---

## Deployment Instructions

### 1. Connect to the Target GKE Cluster

Authenticate and configure `kubectl` to point to your target GKE cluster:

```bash
gcloud container clusters get-credentials [CLUSTER_NAME] \
    --zone [ZONE] \
    --project [PROJECT_ID]
```

### 2. Create the Dedicated Namespace

We recommend deploying Redpanda into a dedicated namespace (e.g., `xmc-metrics`):

```bash
kubectl create namespace xmc-metrics --dry-run=client -o yaml | kubectl apply -f -
kubectl config set-context --current --namespace=xmc-metrics
```

### 3. Setup Helm Repositories

#### Option A: Public Helm Repositories (Requires Direct Internet Access)

Add the official Redpanda Helm repository:

```bash
helm repo add redpanda https://charts.redpanda.com
helm repo update
```

#### Option B: Mirror Charts to Artifact Registry (For Air-Gapped / Restricted Networks)

> [!CAUTION]
> Restricted environments or air-gapped networks may block access to external Helm repositories (`dial tcp: lookup... no such host`). If you cannot access external Helm repositories directly, mirror the chart to Google Cloud Artifact Registry or your enterprise container registry from an internet-enabled environment.

To pull and mirror the chart to your project's Artifact Registry:

```bash
# 1. Authenticate Helm with Google Cloud Artifact Registry
gcloud auth configure-docker us-central1-docker.pkg.dev

# 2. Pull chart (version 5.8.3 packages Redpanda v24.1.x application binary) and push to OCI registry
helm pull redpanda/redpanda --version 5.8.3
helm push redpanda-5.8.3.tgz oci://us-central1-docker.pkg.dev/[PROJECT_ID]/[REPO_NAME]
```

### 4. Customize Configuration (`values.yaml`)

Review and adjust `deploy/addons/metrics/redpanda/values.yaml` as needed for your cluster:
- **`statefulset.replicas`**: Default is `1` for testing and benchmarking.
- **`service.ports`**: Exposes Kafka (`9092`), Schema Registry (`8081`), PandaProxy (`8082`), and Admin (`9644`) ports.
- **`storage`**: Requests a `10Gi` PersistentVolumeClaim using the `standard-rwo` GKE storage class.
- **`resources`**: Configured with `1` CPU core limit and `2Gi` memory limit.
- **`nodeSelector`**: Targets `cloud.google.com/gke-nodepool: main-pool`. Comment this out if your cluster does not use a `main-pool` nodepool.

### 5. Deploy Redpanda

Deploy Redpanda using Helm with the provided `values.yaml`:

```bash
helm install redpanda redpanda/redpanda \
    --namespace xmc-metrics \
    --create-namespace \
    -f values.yaml
```

---

## Verification & Connection Testing

### 1. Verify Pod and Service Status

Ensure the Redpanda pod transitions to `Running` and the service is available:

```bash
kubectl get pods -n xmc-metrics -l app.kubernetes.io/name=redpanda
kubectl get svc -n xmc-metrics
```

### 2. Create Required Topic (`xmc-metrics`)

Exec into the Redpanda pod and create the destination topic for the Go Ingestion Gateway:

```bash
kubectl exec -it -n xmc-metrics redpanda-0 -c redpanda -- rpk topic create xmc-metrics --partitions 1 --replicas 1
kubectl exec -it -n xmc-metrics redpanda-0 -c redpanda -- rpk topic describe xmc-metrics
```

### 3. Test Connection via Port-Forwarding

Forward Kafka (`9092`) and Admin (`9644`) ports to your local workstation:

```bash
kubectl port-forward -n xmc-metrics svc/redpanda 9092:9092 9644:9644
```

Verify cluster health using `rpk`:

```bash
rpk cluster health --brokers localhost:9092
```

### 4. Connect from XMC Ingestion Gateway

The Go Ingestion Gateway ([cmd/ingestion_gateway](../../../../cmd/ingestion_gateway/README.md)) connects using the `-redpanda_brokers` and `-topic_name` flags. When running workloads inside the cluster, configure:

```bash
export REDPANDA_BROKERS="redpanda.xmc-metrics.svc.cluster.local:9092"
export TOPIC_NAME="xmc-metrics"
```

Start the Ingestion Gateway pointing to Redpanda:

```bash
./cmd/ingestion_gateway/ingestion_gateway -redpanda_brokers="${REDPANDA_BROKERS}" -topic_name="${TOPIC_NAME}" -min_partitions=1 -min_replicas=1
```

---

## Cleanup & Uninstallation

To remove the Redpanda release and delete persistent volumes:

```bash
# Uninstall Helm release
helm uninstall redpanda -n xmc-metrics

# Remove PersistentVolumeClaims
kubectl delete pvc -l app.kubernetes.io/instance=redpanda -n xmc-metrics
```
