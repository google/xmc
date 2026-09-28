# XManager on Cloud (XMC) - Helm Chart (`deploy/helm`)

The `xmanager-cloud` Helm chart packages and deploys the core microservices of the XManager on Cloud (XMC) platform onto Kubernetes clusters (such as Google Kubernetes Engine or local KinD).

---

## Architecture Overview

The chart orchestrates the following backend and frontend microservices:

| Microservice | Role & Responsibility | Primary Dependencies |
| :--- | :--- | :--- |
| **`ess`** | Experiment State Server exposing gRPC and HTTP APIs for experiment/work unit lifecycle management. | Cloud Spanner (`experiment-db`), Redis |
| **`actuator`** | Workload creation engine translating pending work units into Kubernetes `JobSet` resources. | Cloud Spanner, Kubernetes API (In-Cluster or Remote), Redis |
| **`reconciler`** | Workload status watcher observing Kubernetes `JobSet` pod events and synchronizing state back to the database. | Cloud Spanner, Kubernetes API (In-Cluster or Remote) |
| **`xid_service`** | Unique distributed identifier generator for experiments and work units. | Cloud Spanner (`xids-db`) |
| **`ui`** | Angular-based web management console. | ESS, Dex / OAuth2 Gateway |
| **`log_monitor`** | Log aggregation and streaming sidecar daemon. | Cloud Logging, Cloud Spanner |

---

## Configuration Reference (`values.yaml`)

### 1. Global & Environment Settings

```yaml
global:
  environment: "dev"       # Deployment environment tag ("dev", "staging", "prod")
  projectId: ""            # Google Cloud Project ID
  region: "us-central1"    # Primary GCP region

isDevEnvironment: false    # Set to true for local development / KinD clusters
```

### 2. Microservice Deployment Settings

Each microservice (`actuator`, `reconciler`, `ess`, `xid`, `ui`, `logMonitor`) can be independently scaled, configured with resource requests/limits, and assigned container image coordinates:

```yaml
actuator:
  enabled: true
  replicas: 1
  image:
    repository: "us-docker.pkg.dev/my-project/xmc/actuator"
    tag: "latest"
    pullPolicy: "IfNotPresent"
  resources:
    limits:
      cpu: "1000m"
      memory: "1Gi"
    requests:
      cpu: "250m"
      memory: "256Mi"
```

---

## Login session lifetime

`auth.sessionTtl` sets the absolute lifetime of both the signed session token and
its browser cookie. The chart passes it to the backend as `SESSION_TTL`. Use Go
duration syntax, for example `168h` for a week or `720h` for thirty days. The
value must be at least `1s`; an invalid value prevents the backend from starting.
The default is defined in `values.yaml` and `defaultSessionTTL` in
`pkg/auth/oauth2/config.go`.

```yaml
auth:
  sessionTtl: "168h"
```

Activity does not extend the lifetime. The setting applies to sessions issued
after the backend restarts; existing tokens retain their signed expiry. Longer
lifetimes also extend the period in which a copied token can be used. Logout
clears the browser cookie without revoking the token, and disabling a Google
account does not invalidate an existing session admitted by the domain allowlist.

---

## Out-of-Cluster & Remote Kubernetes Configuration (`k8s.remote`)

By default, the `actuator` and `reconciler` controllers manage `JobSet` and `Workload` resources on the Kubernetes cluster where they are deployed (using standard in-cluster service account tokens via `rest.InClusterConfig()`).

For hybrid topologies—such as running the control plane locally inside a lightweight KinD cluster while executing real workloads on a remote Google Kubernetes Engine (GKE) cluster—the chart supports mounting an external static kubeconfig.

### Configuration

Set the `k8s.remote` values in your Helm overrides:

```yaml
k8s:
  remote:
    enabled: true                     # Enable out-of-cluster Kubernetes client routing
    secretName: "remote-k8s-config"   # Name of the Kubernetes Secret containing the kubeconfig
    kubeconfigKey: "kubeconfig"       # Key in the Secret holding the raw kubeconfig YAML
```

### How It Works

1. **Volume Mount**: When `k8s.remote.enabled` is `true`, the chart creates a Secret volume mount in both the `actuator` and `reconciler` Deployments:
   - Mount Path: `/etc/remote-k8s`
   - File Path: `/etc/remote-k8s/kubeconfig` (or the key configured in `kubeconfigKey`)
2. **Environment Variable Injection**: Injects `KUBECONFIG=/etc/remote-k8s/kubeconfig` into the controller containers.
3. **Client Initialization**:
   - The Go Kubernetes client loading rules (`cmd/actuator/k8s_client/client.go`) inspect `KUBECONFIG` and fall back to `/etc/remote-k8s/kubeconfig` if present.
   - If `k8s.remote.enabled` is `false`, the controllers communicate strictly with the in-cluster API server via in-cluster service account tokens.
4. **Token Authentication Parity**: Because the target kubeconfig uses a static ServiceAccount bearer token minted on the remote cluster (e.g. via `kubectl create token actuator-service-account`), no Google Cloud SDK or `gcloud` binaries are required inside the controller images.

### Creating the Remote Kubeconfig Secret Manually

If you are deploying Helm manually rather than using `tools/dev_env/dev_env.py up --remote-k8s`, provision the Secret before installing the chart:

```bash
kubectl create secret generic remote-k8s-config \
  --namespace xmc \
  --from-file=kubeconfig=/path/to/target/cluster/kubeconfig
```

---

## Deployment Modes

### Local KinD Deployment

When deploying locally with KinD, use the local development values profile:

```bash
helm upgrade --install xmc deploy/helm \
  -n xmc \
  --create-namespace \
  -f deploy/helm/values.yaml \
  -f deploy/helm/values-local.yaml
```

### Production GKE Deployment

Production installations typically deploy via HPC Cluster Toolkit blueprints (`deploy/ctk_v2/`) which render values dynamically from `xmanager-cloud-deployment.yaml`.
