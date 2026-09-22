# Redpanda 3P Wrapper Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/redpanda/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) wrapping an upstream **Redpanda Kubernetes Operator** cluster (`clusters.redpanda.vectorized.io`).

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the Redpanda blueprint hardcoded in [`templates/component.yaml`](templates/component.yaml) and are **not** configurable by users via `values.yaml`:

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"redpanda"` | Canonical catalog identifier referenced by dependent components (e.g., `dependencies: [redpanda]`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"redpanda"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | Hardcoded as a 3P Wrapper Component (`Redpanda` is always a third-party operator/workload wrapper, never a 1P XMC component). |
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

### Backing Operator Reference (`wrapperRef`)

`wrapperRef` tells the XMC GitOps Bridge Controller which third-party Kubernetes Custom Resource represents the running Redpanda cluster and how to extract its health and endpoint coordinates:

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `wrapperRef.group` | `string` | `"redpanda.vectorized.io"` | Kubernetes API group of the upstream Redpanda Operator CRD (`clusters.redpanda.vectorized.io`). |
| `wrapperRef.version` | `string` | `"v1alpha1"` | Kubernetes API version of the Redpanda Operator `Cluster` CRD. |
| `wrapperRef.kind` | `string` | `"Cluster"` | Kubernetes `Kind` defined by the Redpanda Operator for a broker cluster instance (`Cluster`, not a Kubernetes-native resource). |
| `wrapperRef.name` | `string` | `"redpanda"` | `metadata.name` of the target `Cluster.redpanda.vectorized.io` CR in the cluster. Override if your Redpanda `Cluster` CR uses a custom name. |
| `wrapperRef.namespace` | `string` | `"xmc"` | `metadata.namespace` of the target `Cluster.redpanda.vectorized.io` CR. |
| `wrapperRef.observedGenerationPath` | `string` | `"$.status.observedGeneration"` | JSONPath on the `Cluster` CR status used to confirm the operator has reconciled the latest spec generation. |
| `wrapperRef.readyConditionPath` | `string` | `"$.status.conditions[?(@.type=='Ready')].status"` | JSONPath on the `Cluster` CR status evaluated by the Bridge Controller to determine readiness. |
| `wrapperRef.expectedReadyValue` | `string` | `"True"` | Expected string value at `readyConditionPath` when the Redpanda cluster is healthy. |
| `wrapperRef.endpointJsonPath` | `string` | `"$.status.nodes.internal[0]"` | JSONPath on the `Cluster` CR status from which the internal broker address is extracted. |
| `wrapperRef.connectionSecretJsonPath` | `string` | *(omitted)* | Optional JSONPath to a credentials Secret reference on the `Cluster` CR status when SASL/TLS authentication is enabled. |

### Broker Connection & Sizing Parameters (`parameters`)

All values under `spec.parameters` are rendered as strings in [`templates/component.yaml`](templates/component.yaml) to satisfy the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`):

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `parameters.brokerEndpoints` | `string` | `"redpanda-0.redpanda.xmc.svc.cluster.local:9092"` | Internal Kubernetes headless service DNS endpoint(s) for Kafka clients. |
| `parameters.kafkaPort` | `int` / `string` | `9092` (rendered as `"9092"`) | Kafka protocol listener port (converted from numeric `9092` in `values.yaml` to string `"9092"` via `toString` and `quote`). |
| `parameters.defaultTopic` | `string` | `"xmc-metrics"` | Default Kafka topic for XMC telemetry and event ingestion. |
| `parameters.storageSize` | `string` | `"10Gi"` | Persistent volume claim size per Redpanda broker pod. |

---

## 3. Downstream Consumer Discovery (`ingestion-gateway`)

When `componentsDeployment.enabled` (`enable_components`) is `true`, `ingestion-gateway` queries the `redpanda` `Component` CR (`xmc.dev/v1alpha1`) in `COMPONENTS_NAMESPACE` (using the namespace-scoped `ingestion-gateway-components-reader` `Role`) at startup:

1. **Broker Resolution Precedence**:
   - **Primary (`status.access.envVars`)**: Reads `status.access.envVars["ENDPOINT"]` (populated by `xmc-bridge` from `wrapperRef.endpointJsonPath`), appending `status.access.envVars["PORT"]` or `spec.parameters["kafkaPort"]` if `ENDPOINT` does not already include a port.
   - **Fallback (`spec.parameters`)**: If `status.access.envVars["ENDPOINT"]` is not yet populated, falls back to `spec.parameters["brokerEndpoints"]`.
2. **Default Topic Resolution**:
   - Overrides `-topic_name` with `spec.parameters["defaultTopic"]` when non-empty.
3. **Graceful Fallback Contract**:
   - When `enable_components=false`, or when `Component/redpanda` is absent or disabled (`spec.enabled: false` / `status.state: COMPONENT_STATE_DISABLED`), `ingestion-gateway` retains its static CLI flag values (`-redpanda_brokers`, `-topic_name`).

---

## 4. Configuration & Rendered Manifest Examples

### Example 1: Minimum Valid User Configuration (`values.yaml` Without Optional Overrides)

Because `metadata.name`, `spec.componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER`, `spec.componentClass: "event-stream"`, `spec.version`, and all default `wrapperRef` and `parameters` fields are pre-configured by the chart, the **minimum valid user `values.yaml` override** required to activate the Redpanda component is simply:

```yaml
enabled: true
```

Or via CLI:

```bash
helm template redpanda deploy/profiles/stable-2026a/components/redpanda \
  --set enabled=true
```

### Example 2: Custom User Configuration (`values.yaml` With Optional Overrides)

To target a custom Redpanda instance or Kubernetes `StatefulSet` (`apps/v1`) and custom broker/topic settings:

```yaml
enabled: true
namespace: xmc
deletionProtection: true
deletionPolicy: Delete
wrapperRef:
  group: "apps"
  version: "v1"
  kind: "StatefulSet"
  name: "redpanda"
  namespace: "xmc"
  readyConditionPath: "$.status.readyReplicas"
  expectedReadyValue: "1"
parameters:
  brokerEndpoints: "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
  kafkaPort: 9092
  defaultTopic: "xmc-metrics"
  storageSize: "10Gi"
```

### Example 3: Rendered Kubernetes `Component` Manifest Output

Running `helm template redpanda deploy/profiles/stable-2026a/components/redpanda --set enabled=true` produces the following `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`), with `spec.componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER` and `spec.componentClass: "event-stream"` automatically enforced by [`templates/component.yaml`](templates/component.yaml):

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
  wrapperRef:
    group: "redpanda.vectorized.io"
    version: "v1alpha1"
    kind: "Cluster"
    name: "redpanda"
    namespace: "xmc"
    observedGenerationPath: "$.status.observedGeneration"
    readyConditionPath: "$.status.conditions[?(@.type=='Ready')].status"
    expectedReadyValue: "True"
    endpointJsonPath: "$.status.nodes.internal[0]"
  parameters:
    brokerEndpoints: "redpanda-0.redpanda.xmc.svc.cluster.local:9092"
    kafkaPort: "9092"
    defaultTopic: "xmc-metrics"
    storageSize: "10Gi"
```
