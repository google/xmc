# Redpanda 3P Wrapper Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/redpanda/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) wrapping an upstream **Redpanda Kubernetes Operator** cluster (`clusters.redpanda.vectorized.io`).

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the Redpanda blueprint in [`templates/component.yaml`](templates/component.yaml) and are **not** configurable via `values.yaml`:

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"redpanda"` | Canonical catalog identifier referenced by dependent components (e.g., `dependencies: [redpanda]`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"redpanda"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | Declares that Redpanda is managed via an external 3P Kubernetes Operator CR (`spec.wrapperRef`). |

---

## 2. Admin & Chart Configuration Reference ([`values.yaml`](values.yaml))

### Top-Level Lifecycle & Catalog Settings

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `bool` | `false` | Enables or disables the Redpanda component in the XMC catalog. Defaults to `false` so non-component deployments (`enable_components: false`) are unaffected. |
| `namespace` | `string` | `"xmc"` | Namespace where the `Component` CR is installed. |
| `componentClass` | `string` | `"event-stream"` | Logical service class used by XMC workloads to discover the event-streaming broker. |
| `chartVersion` | `string` | `.Chart.Version` (`"0.1.0"`) | Optional override for `spec.chartVersion`. Omitted from `values.yaml` by default so [`Chart.yaml`](Chart.yaml) (`version: 0.1.0`) serves as the single source of truth. |
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

## 4. Rendered Manifest & `wrapperRef` Configuration Examples

### Example 1: Default Upstream Redpanda Operator (`Cluster.redpanda.vectorized.io`)

Render the manifest with `enabled=true`:

```bash
helm template redpanda deploy/profiles/stable-2026a/components/redpanda \
  --set enabled=true
```

Rendered valid `Component` manifest (`apiVersion: xmc.dev/v1alpha1`):

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

### Example 2: Kubernetes `StatefulSet` Override (`StatefulSet.apps/v1`)

For environments where Redpanda is deployed as a standard Kubernetes `StatefulSet` (`apps/v1`, `metadata.name: redpanda`):

```bash
helm template redpanda deploy/profiles/stable-2026a/components/redpanda \
  --set enabled=true \
  --set wrapperRef.group=apps \
  --set wrapperRef.version=v1 \
  --set wrapperRef.kind=StatefulSet \
  --set wrapperRef.readyConditionPath='$.status.readyReplicas' \
  --set-string wrapperRef.expectedReadyValue="1"
```

Rendered `spec.wrapperRef` section:

```yaml
  wrapperRef:
    group: "apps"
    version: "v1"
    kind: "StatefulSet"
    name: "redpanda"
    namespace: "xmc"
    observedGenerationPath: "$.status.observedGeneration"
    readyConditionPath: "$.status.readyReplicas"
    expectedReadyValue: "1"
    endpointJsonPath: "$.status.nodes.internal[0]"
```
