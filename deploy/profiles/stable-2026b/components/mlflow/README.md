# MLflow 3P Wrapper Component Blueprint (`stable-2026b`)

This Helm sub-chart (`deploy/profiles/stable-2026b/components/mlflow/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) for a bring-your-own **MLflow 3.x Tracking Server** (`experiment-tracker`).

Because MLflow is deployed as an external or in-cluster HTTP service without a Kubernetes operator or CRD, this blueprint omits `spec.wrapperRef`. `xmc-bridge` verifies readiness by probing `parameters.healthCheckEndpoint` (defaulting to `<trackingUri>/health`) and publishes connection metadata (`TRACKING_URI`, `UI_BASE_URL`, `SECRET_NAME`, and Secret key names) in `status.access.envVars`.

> [!NOTE]
> **Bring your own MLflow.** XMC does not install, run, or upgrade MLflow in production: operators bring their own MLflow 3.x tracking server and point `parameters.trackingUri` at it. A dev/test-only fixture for KinD and dev environments lives in [`test/e2e/testdata/mlflow`](../../../../../test/e2e/testdata/mlflow/README.md).

---

## 1. Fixed Blueprint Identity & Contract Fields

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"mlflow"` | Canonical catalog identifier for the MLflow tracking component. |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026b"` | Binds this component definition to the `stable-2026b` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"mlflow"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | 3P Wrapper Component for a bring-your-own MLflow tracking server. |
| `spec.componentClass` | `"experiment-tracker"` | Canonical service class for experiment tracking. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced from [`Chart.yaml`](Chart.yaml). |
| `spec.stability` | `"EXPERIMENTAL"` | Catalog-declared maturity stage; enabling requires `acknowledgeExperimental: true`. |

---

## 2. Admin & Chart Configuration Reference ([`values.yaml`](values.yaml))

### Top-Level Lifecycle & Catalog Settings

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `bool` | `false` | Enables or disables the MLflow component. Defaults to `false`. |
| `acknowledgeExperimental` | `bool` | *(omitted)* | Required (`true`) when `enabled: true` because `stability` is `EXPERIMENTAL`. |
| `namespace` | `string` | `""` (`.Release.Namespace`, or `"xmc"` when `"default"`/`""`) | Namespace where the `Component` CR is installed. |
| `deletionProtection` | `bool` | `true` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `string` | `"Delete"` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `list[string]` | `[]` | Optional list of prerequisite XMC Component names. |

### Connection & Authentication Parameters (`parameters`)

All values under `spec.parameters` are rendered as strings in [`templates/component.yaml`](templates/component.yaml) to satisfy the `components.xmc.dev` CRD schema.

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `parameters.trackingUri` | `string` | `"http://mlflow-service.xmc.svc.cluster.local:5000"` | MLflow tracking server URI used by XMC services and training pods. |
| `parameters.uiBaseUrl` | `string` | `""` (defaults to `trackingUri`) | Browser-facing MLflow UI base URL used for UI links. |
| `parameters.secretName` | `string` | `""` (omitted) | Kubernetes `Secret` name holding MLflow credentials. Leave empty for an unauthenticated server. |
| `parameters.secretTokenKey` | `string` | `""` (omitted) | Key in `secretName` holding a bearer token (`MLFLOW_TRACKING_TOKEN`). |
| `parameters.secretUsernameKey` | `string` | `""` (omitted) | Key in `secretName` holding a basic-auth username (`MLFLOW_TRACKING_USERNAME`). |
| `parameters.secretPasswordKey` | `string` | `""` (omitted) | Key in `secretName` holding a basic-auth password (`MLFLOW_TRACKING_PASSWORD`). |
| `parameters.healthCheckEndpoint` | `string` | `""` (defaults to `<trackingUri>/health`) | HTTP endpoint probed by `xmc-bridge` to determine component readiness. |

#### Health Probing Without Credentials

`xmc-bridge` probes `parameters.healthCheckEndpoint` **without credentials** (an unauthenticated HTTP `GET`). If your MLflow server protects `/health` behind authentication, set `parameters.healthCheckEndpoint` to an unauthenticated health path on your server or ingress proxy.

#### Credentials Secret Format

Leave `secretName` empty for an unauthenticated MLflow server. When authentication is enabled, create a Kubernetes `Secret` in the XMC namespace (and in each MultiKueue worker cluster namespace where training pods run) using either:

- **Bearer token authentication**: set `secretName` and `secretTokenKey` (e.g., key `token`).
- **Basic authentication**: set `secretName`, `secretUsernameKey`, and `secretPasswordKey` (e.g., keys `username` and `password`).

`xmc-bridge` publishes only the Secret name and key names in `status.access.envVars`; Secret values are never read, logged, or published by `xmc-bridge`.

#### MultiKueue Clusters

In MultiKueue deployments, training pods may run on remote worker clusters:

- `parameters.trackingUri` must be network-reachable from both the XMC control-plane cluster and all MultiKueue worker clusters.
- The credentials `Secret` (`parameters.secretName`) must exist in every cluster/namespace where training pods run. XMC does not replicate Secrets across clusters.

---

## 3. Configuration & Rendered Manifest Examples

> [!NOTE]
> Admins enable MLflow through the [`stable-2026b` profile](../../README.md) by setting `components.mlflow.enabled=true` and `components.mlflow.acknowledgeExperimental=true`.

### Example 1: Unauthenticated MLflow Server

```yaml
components:
  mlflow:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      trackingUri: "http://mlflow-service.xmc.svc.cluster.local:5000"
```

### Example 2: Authenticated MLflow Server with Separate UI URL

```yaml
components:
  mlflow:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      trackingUri: "https://mlflow.internal.example.com"
      uiBaseUrl: "https://mlflow.example.com"
      secretName: "mlflow-credentials"
      secretTokenKey: "token"
      healthCheckEndpoint: "https://mlflow.internal.example.com/healthz"
```

### Example 3: Standalone Helm Preview

```bash
helm template mlflow deploy/profiles/stable-2026b/components/mlflow \
  --set enabled=true \
  --set acknowledgeExperimental=true
```
