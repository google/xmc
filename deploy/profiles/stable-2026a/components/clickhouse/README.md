# ClickHouse 3P Wrapper Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/clickhouse/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) that provides XMC services (`pkg/clickhouse`, `dashboard-service`) with ClickHouse connection coordinates, credentials Secret references, and health probe endpoints.

Because ClickHouse is a network analytical database dependency (`analytical-db`), the component contract requires no Kubernetes operator or CRD reference (`wrapperRef`); `xmc-bridge` verifies readiness directly via active HTTP health probing (`parameters.healthCheckEndpoint`) and publishes connection and Secret coordinates to downstream XMC consumers.

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants encapsulated in [`templates/component.yaml`](templates/component.yaml) and omitted from [`values.yaml`](values.yaml):

| Manifest Field | Encapsulated Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"clickhouse"` | Canonical catalog identifier referenced by dependent components (e.g., `dependencies: [clickhouse]`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"clickhouse"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | Hardcoded as a 3P Wrapper Component (`ClickHouse` is always an external/third-party database service, never a 1P XMC microservice). |
| `spec.componentClass` | `"analytical-db"` | Canonical service class used by XMC workloads to discover the analytical database. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |

---

## 2. Admin & Chart Configuration Reference ([`values.yaml`](values.yaml))

### Top-Level Lifecycle & Catalog Settings

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `bool` | `false` | Enables or disables the ClickHouse component in the XMC catalog. Defaults to `false` so non-component deployments (`enable_components: false`) are unaffected. |
| `namespace` | `string` | `"xmc"` | Namespace where the `Component` CR is installed. |
| `deletionProtection` | `bool` | `true` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `string` | `"Delete"` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `list[string]` | `[]` | Optional list of prerequisite XMC Component names that must be healthy before ClickHouse is reconciled. |

### Connection, Authentication Secret & Health Parameters (`parameters`)

All values under `spec.parameters` are rendered as strings in [`templates/component.yaml`](templates/component.yaml) to satisfy the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`):

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `parameters.host` | `string` | `"clickhouse.xmc-metrics.svc.cluster.local"` | ClickHouse hostname or Kubernetes service FQDN (maps to `CLICKHOUSE_HOST`). |
| `parameters.clusterEndpoints` | `string` | `""` (renders as `"<host>:<nativePort>"`) | Internal Kubernetes DNS endpoint(s) for ClickHouse native protocol clients. Dynamically constructed from `host` and `nativePort` when empty. |
| `parameters.nativePort` | `int` / `string` | `9000` (rendered as `"9000"`) | ClickHouse native TCP protocol port (maps to `CLICKHOUSE_PORT`; coerced to string `"9000"` via `toString | quote`). |
| `parameters.httpPort` | `int` / `string` | `8123` (rendered as `"8123"`) | ClickHouse HTTP interface port (used for HTTP queries and `/ping` health probes; coerced to string `"8123"` via `toString | quote`). |
| `parameters.defaultDatabase` | `string` | `"default"` | Default ClickHouse database for XMC metrics ingestion and analytical queries (maps to `CLICKHOUSE_DATABASE`). |
| `parameters.user` | `string` | `"default"` | ClickHouse login username (maps to `CLICKHOUSE_USER`). |
| `parameters.secretName` | `string` | `"clickhouse-secret"` | Kubernetes `Secret` name holding the ClickHouse password (maps to `CLICKHOUSE_PASSWORD` `secretKeyRef`—never plaintext credentials). |
| `parameters.secretPasswordKey` | `string` | `"clickhouse-password"` | Key inside `parameters.secretName` holding the ClickHouse password. |
| `parameters.healthCheckEndpoint` | `string` | `""` (renders as `"http://<host>:<httpPort>/ping"`) | HTTP health check endpoint probed by `xmc-bridge` to verify ClickHouse readiness. Dynamically constructed from `host` and `httpPort` when empty. |
| `parameters.storageSize` | `string` | `"10Gi"` | Persistent volume claim size per ClickHouse replica pod. |

---

## 3. Configuration & Rendered Manifest Examples

### Example 1: Minimum Valid User Configuration (`values.yaml` Without Optional Overrides)

Because all identity and default connection/credentials `parameters` fields are pre-configured by the chart, the **minimum valid user `values.yaml` override** required to activate the ClickHouse component is simply:

```yaml
enabled: true
```

Or via CLI:

```bash
helm template clickhouse deploy/profiles/stable-2026a/components/clickhouse \
  --set enabled=true
```

### Example 2: Custom User Configuration (`values.yaml` With Connection & Credentials Overrides)

To point XMC at a custom ClickHouse endpoint, login user, password Secret, and health check URL:

```yaml
enabled: true
namespace: xmc-metrics
deletionProtection: true
deletionPolicy: Delete
parameters:
  host: "clickhouse-custom.xmc-metrics.svc.cluster.local"
  clusterEndpoints: "clickhouse-custom.xmc-metrics.svc.cluster.local:9440"
  nativePort: 9440
  httpPort: 8443
  defaultDatabase: "analytics"
  user: "xmc_reader"
  secretName: "clickhouse-custom-secret"
  secretPasswordKey: "password"
  healthCheckEndpoint: "https://clickhouse-custom.xmc-metrics.svc.cluster.local:8443/ping"
  storageSize: "100Gi"
```

### Example 3: Rendered Kubernetes `Component` Manifest Output

Running `helm template clickhouse deploy/profiles/stable-2026a/components/clickhouse --set enabled=true` produces the following `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`):

```yaml
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: "clickhouse"
  namespace: "xmc"
  labels:
    xmc.dev/profile: "stable-2026a"
    app.kubernetes.io/name: "clickhouse"
    app.kubernetes.io/managed-by: "Helm"
spec:
  enabled: true
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  componentClass: "analytical-db"
  version: "0.1.0"
  chartVersion: "0.1.0"
  deletionProtection: true
  deletionPolicy: "Delete"
  parameters:
    host: "clickhouse.xmc-metrics.svc.cluster.local"
    clusterEndpoints: "clickhouse.xmc-metrics.svc.cluster.local:9000"
    nativePort: "9000"
    httpPort: "8123"
    defaultDatabase: "default"
    user: "default"
    secretName: "clickhouse-secret"
    secretPasswordKey: "clickhouse-password"
    healthCheckEndpoint: "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping"
    storageSize: "10Gi"
```

---

## 4. Downstream Consumer Resolution (`dashboard-service` & `pkg/clickhouse/componentconfig`)

When `enable_components` is `true`, `dashboard-service` resolves ClickHouse connection options from the `clickhouse` `Component` CR via [`pkg/clickhouse/componentconfig`](../../../../pkg/clickhouse/componentconfig/resolver.go):

1. **Endpoint Precedence**: Reads `status.access.envVars` (`HOST` / `PORT` or `ENDPOINT`) published by `xmc-bridge` first, falling back to `spec.parameters` (`clusterEndpoints` or `host` + `nativePort`, default `9000`).
2. **Database & User**: Reads `spec.parameters.defaultDatabase` (default `"default"`) and `spec.parameters.user` (default `"default"`).
3. **Password Resolution**:
   - **Secret Manager CSI Mode (`CLICKHOUSE_PASSWORD_FILE` set)**: Reads the password from the CSI-mounted file (`CLICKHOUSE_PASSWORD_FILE`), taking precedence over Kubernetes Secret lookups.
   - **Kubernetes Secret Mode (CSI disabled)**: Reads the Secret named in `status.access.envVars.SECRET_NAME` (or `spec.parameters.secretName`, default `"clickhouse-secret"`) under key `spec.parameters.secretPasswordKey` (default `"clickhouse-password"`) in the component namespace.
4. **Fallback & Failure Semantics**: Static `CLICKHOUSE_*` environment variables are used as a fallback **only** when the `clickhouse` `Component` CR is `NotFound` or disabled (`spec.enabled: false` or `COMPONENT_STATE_DISABLED` / `DISABLING` / `DELETED`). Any Kubernetes API error, malformed endpoint/port parameter, or missing/unreadable password Secret surfaces as a hard error so `/readyz` reports unready rather than connecting with stale credentials.

### 4.1 Dynamic Reconfiguration & Hot-Swapping (Zero Pod Restarts)

When ClickHouse parameters or credentials change in Kubernetes, `dashboard-service` dynamically detects the changes and reconfigures its data engine without requiring pod restarts or manual rollouts:

1. **Deterministic Fingerprinting**: `dashboard-service` computes a deterministic SHA-256 fingerprint over the resolved options (`OptionsFingerprint(opts)`), hashing endpoints, database, username, and password.
2. **Readiness Probe Detection (`/readyz`)**: During every periodic readiness health probe (every 10 seconds), the dynamic provider resolves the latest `Component` CR and Secret values, compares the newly computed fingerprint with the active connection's fingerprint, and triggers a live swap if a difference is detected.
3. **Zero-Downtime Atomic Swap**: The new connection is dialed and verified (`conn.Ping(ctx)`) before replacing the active engine. If successful, the engine and connection are atomically updated under write lock, and the previous connection is closed gracefully.
4. **Lock-Free Query Fast-Path**: Chart queries (`GetExperimentSummary`, `QueryTimeSeries`) acquire a shared read lock and access the cached data engine directly. The query hot path never makes Kubernetes API calls, ensuring zero added latency or load on the API server.
5. **Fail-Safe Fallback**: If an updated `Component` CR or rotated Secret contains invalid credentials or unreachable endpoints, the rebuild attempt fails safely: `dashboard-service` continues serving queries using the existing established connection while reporting an unready state on `/readyz` to alert operators.
