# Redpanda 3P Wrapper Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/redpanda/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) for the **Redpanda** event-streaming broker (`event-stream`).

Because Redpanda is a network event-streaming dependency (`event-stream`), the component contract requires no Kubernetes operator or CRD reference (`wrapperRef`); `xmc-bridge` verifies readiness directly via active TCP health probing (`parameters.brokerEndpoints`) and publishes broker coordinates to downstream XMC consumers (`ingestion-gateway`).

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the Redpanda blueprint hardcoded in [`templates/component.yaml`](templates/component.yaml) and are **not** configurable by users via `values.yaml`:

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"redpanda"` | Canonical catalog identifier referenced by dependent components (e.g., `dependencies: [redpanda]`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"redpanda"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | Hardcoded as a 3P Wrapper Component (`Redpanda` is always a third-party event-streaming service, never a 1P XMC component). |
| `spec.componentClass` | `"event-stream"` | Canonical service class used by XMC workloads to discover the event-streaming broker. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |

---

## 2. Admin & Chart Configuration Reference ([`values.yaml`](values.yaml))

### Top-Level Lifecycle & Catalog Settings

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `bool` | `false` | Enables or disables the Redpanda component in the XMC catalog. Defaults to `false` so non-component deployments (`enable_components: false`) are unaffected. |
| `namespace` | `string` | `"xmc"` | Namespace where the `Component` CR is installed. |
| `deletionProtection` | `bool` | `true` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `string` | `"Delete"` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `list[string]` | `[]` | Optional list of prerequisite XMC Component names that must be healthy before Redpanda is reconciled. |

### Broker Connection & Sizing Parameters (`parameters`)

All values under `spec.parameters` are rendered as strings in [`templates/component.yaml`](templates/component.yaml) to satisfy the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`):

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `parameters.brokerEndpoints` | `string` | `"redpanda-0.redpanda.xmc.svc.cluster.local:9092"` | Internal Kubernetes headless service DNS endpoint(s) for Kafka clients and TCP health probing. |
| `parameters.kafkaPort` | `int` / `string` | `9092` (rendered as `"9092"`) | Kafka protocol listener port (converted from numeric `9092` in `values.yaml` to string `"9092"` via `toString` and `quote`). |
| `parameters.defaultTopic` | `string` | `"xmc-metrics"` | Default Kafka topic for XMC telemetry and event ingestion. |
| `parameters.storageSize` | `string` | `"10Gi"` | Persistent volume claim size per Redpanda broker pod. |

---

## 3. Downstream Consumer Discovery (`ingestion-gateway`)

When `componentsDeployment.enabled` (`enable_components`) is `true`, `ingestion-gateway` queries the `redpanda` `Component` CR (`xmc.dev/v1alpha1`) in `COMPONENTS_NAMESPACE` (using the namespace-scoped `ingestion-gateway-components-reader` `Role`) at startup:

1. **Broker Resolution Precedence**:
   - **Primary (`status.access.envVars`)**: Reads `status.access.envVars["ENDPOINT"]` (populated by `xmc-bridge` from `parameters.brokerEndpoints`), appending `status.access.envVars["PORT"]` or `spec.parameters["kafkaPort"]` if `ENDPOINT` does not already include a port.
   - **Fallback (`spec.parameters`)**: If `status.access.envVars["ENDPOINT"]` is not yet populated, falls back to `spec.parameters["brokerEndpoints"]`.
2. **Default Topic Resolution**:
   - Overrides `-topic_name` with `spec.parameters["defaultTopic"]` when non-empty.
3. **Graceful Fallback Contract**:
   - When `enable_components=false`, or when `Component/redpanda` is absent or disabled (`spec.enabled: false` / `status.state: COMPONENT_STATE_DISABLED`), `ingestion-gateway` retains its static CLI flag values (`-redpanda_brokers`, `-topic_name`).

---

## 4. Configuration & Rendered Manifest Examples

### Example 1: Minimum Valid User Configuration (`values.yaml` Without Optional Overrides)

Because `metadata.name`, `spec.componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER`, `spec.componentClass: "event-stream"`, `spec.version`, and all default `parameters` fields are pre-configured by the chart, the **minimum valid user `values.yaml` override** required to activate the Redpanda component is simply:

```yaml
enabled: true
```

Or via CLI:

```bash
helm template redpanda deploy/profiles/stable-2026a/components/redpanda \
  --set enabled=true
```

### Example 2: Custom User Configuration (`values.yaml` With Optional Overrides)

To target a custom Redpanda broker endpoint and custom topic/storage settings:

```yaml
enabled: true
namespace: xmc
deletionProtection: true
deletionPolicy: Delete
parameters:
  brokerEndpoints: "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
  kafkaPort: 9092
  defaultTopic: "xmc-metrics"
  storageSize: "10Gi"
```

### Example 3: Rendered Kubernetes `Component` Manifest Output

Running `helm template redpanda deploy/profiles/stable-2026a/components/redpanda --set enabled=true` produces the following `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`):

```yaml
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: "redpanda"
  namespace: "xmc"
  labels:
    xmc.dev/profile: "stable-2026a"
    app.kubernetes.io/name: "redpanda"
    app.kubernetes.io/managed-by: "Helm"
spec:
  enabled: true
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  componentClass: "event-stream"
  version: "0.1.0"
  chartVersion: "0.1.0"
  deletionProtection: true
  deletionPolicy: "Delete"
  parameters:
    brokerEndpoints: "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
    kafkaPort: "9092"
    defaultTopic: "xmc-metrics"
    storageSize: "10Gi"
```
