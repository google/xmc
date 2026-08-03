# ClickHouse Addon Deployment Guide for GKE

This directory contains the documentation and example Helm configuration (`values.yaml`) for deploying **ClickHouse** onto a Google Kubernetes Engine (GKE) cluster.

ClickHouse serves as the high-performance analytical database backend for XManager on Cloud (XMC) metrics system testing and Flatboard-like visualization in Grafana.

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

We recommend deploying ClickHouse into a dedicated namespace (e.g., `xmc-metrics`):

```bash
kubectl create namespace xmc-metrics --dry-run=client -o yaml | kubectl apply -f -
kubectl config set-context --current --namespace=xmc-metrics
```

### 3. Setup Helm Repositories

#### Option A: Public Helm Repositories (Requires Direct Internet Access)

Add the official ClickHouse / ClickStack Helm repositories:

```bash
helm repo add clickhouse https://charts.clickhouse.com
helm repo add clickstack https://clickhouse.github.io/ClickStack-helm-charts
helm repo update
```

#### Option B: Mirror Charts to Artifact Registry (For Restricted Networks)

> [!CAUTION]
> Workstations on restricted networks or private cloud environments may block access to external Helm repositories (`dial tcp: lookup... no such host`). If you cannot access external Helm repositories directly, mirror the chart to Google Cloud Artifact Registry from an internet-enabled environment.

To pull and mirror the chart to your project's Artifact Registry:

```bash
# 1. Authenticate Helm with Google Cloud Artifact Registry
gcloud auth configure-docker us-central1-docker.pkg.dev

# 2. Pull chart and push to OCI registry
helm pull clickhouse/clickhouse --version 3.4.0
helm push clickhouse-3.4.0.tgz oci://us-central1-docker.pkg.dev/[PROJECT_ID]/[REPO_NAME]
```

### 4. Customize Configuration (`values.yaml`)

Review and adjust `deploy/addons/metrics/clickhouse/values.yaml` as needed for your cluster:
- **`replicaCount`**: Default is `1` for testing and benchmarking.
- **`service.ports`**: Exposes HTTP (`8123`) and native TCP (`9000`) ports.
- **`persistence`**: Requests a `10Gi` PersistentVolumeClaim using the `standard-rwo` GKE storage class.
- **`resources`**: Configured with `2` CPU / `4Gi` memory limit and `100m` CPU / `512Mi` memory request.
- **`nodeSelector`**: Targets `cloud.google.com/gke-nodepool: main-pool`. Comment this out if your cluster does not use a `main-pool` nodepool.

### 5. Deploy ClickHouse

Deploy ClickHouse using Helm with the provided `values.yaml`:

```bash
# get chart
helm repo add clickstack https://clickhouse.github.io/ClickStack-helm-charts
helm repo update

# install operators
helm install clickstack-operators clickstack/clickstack-operators --namespace xmc-metrics --create-namespace -f values.yaml 

# install clickhouse
helm install my-clickstack clickstack/clickstack
```

---

## Verification & Connection Testing

### 1. Verify Pod and Service Status

Ensure the ClickHouse pod transitions to `Running` and the ClusterIP service is available:

```bash
kubectl get pods -n xmc-metrics -l app.kubernetes.io/name=clickhouse
kubectl get svc -n xmc-metrics
```

### 2. Test Connection via Port-Forwarding

Forward HTTP (`8123`) and TCP (`9000`) ports to your local workstation:

```bash
kubectl port-forward -n xmc-metrics svc/clickhouse 8123:8123 9000:9000
```

Verify server health using `curl`:

```bash
curl http://localhost:8123/ping
# Output should be: Ok

curl "http://localhost:8123/?query=SELECT%20version()"
```

### 3. Connect from XMC Backend

The XMC backend ClickHouse client ([pkg/clickhouse](../../../../pkg/clickhouse/clickhouse.go)) connects using standard environment variables. When running workloads inside the cluster, configure:

```bash
export CLICKHOUSE_HOST="clickhouse.xmc-metrics.svc.cluster.local"
export CLICKHOUSE_PORT="9000"
export CLICKHOUSE_DATABASE="default"
export CLICKHOUSE_USER="default"
export CLICKHOUSE_PASSWORD=$(kubectl get secret clickhouse -n xmc-metrics -o jsonpath="{.data.clickhouse-password}" | base64 --decode)
```

Or connect via DSN:

```bash
export CLICKHOUSE_DSN="clickhouse://default:@clickhouse.xmc-metrics.svc.cluster.local:9000/default"
```

---

## Cleanup & Uninstallation

To remove the ClickHouse release and delete persistent volumes:

```bash
# Uninstall Helm release
helm uninstall clickhouse -n xmc-metrics

# Remove PersistentVolumeClaims
kubectl delete pvc -l app.kubernetes.io/instance=clickhouse -n xmc-metrics
```
