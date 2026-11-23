# Vizier 3P Wrapper Component Blueprint (`stable-2026b`)

This Helm sub-chart (`deploy/profiles/stable-2026b/components/vizier/`) renders the XMC `Component` Custom Resource (`apiVersion: xmc.dev/v1alpha1`, `kind: Component`) for the **Open Source Vizier** hyperparameter optimization engine (`optimization-engine`).

Because Open Source Vizier is deployed as a bring-your-own (BYO) gRPC service (`vizier-service:7077`) without a custom Kubernetes operator or CRD, this blueprint omits `spec.wrapperRef`. Instead, `xmc-bridge` verifies readiness directly via active health probing (TCP against `parameters.endpoint` by default, or gRPC health checking when `parameters.healthCheckEndpoint` opts in) and publishes resolved connection coordinates (`status.access.envVars.ENDPOINT`, `HOST`, `PORT`) to downstream consumers (`hpo` / `xmc-study-controller`).

> [!NOTE]
> **Bring your own Vizier.** XMC does not run Vizier: operators provide an Open Source Vizier gRPC endpoint and set `parameters.endpoint` to it. A dev/test-only fixture for KinD and dev environments lives in [`test/e2e/testdata/vizier`](../../../../../test/e2e/testdata/vizier/README.md).

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the Vizier blueprint defined in [`templates/component.yaml`](templates/component.yaml):

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"vizier"` | Canonical catalog identifier referenced by dependent components (`dependencies: [vizier, clickhouse]`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026b"` | Binds this component definition to the `stable-2026b` release profile. |
| `metadata.labels["app.kubernetes.io/name"]` | `"vizier"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_3P_WRAPPER` | Hardcoded as a 3P Wrapper Component for the external/BYO Open Source Vizier service. |
| `spec.componentClass` | `"optimization-engine"` | Canonical service class used by XMC HPO workloads to discover the optimization engine. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |
| `spec.stability` | `"EXPERIMENTAL"` | Catalog-declared maturity stage; enabling requires `acknowledgeExperimental: true`. |

---

## 2. Admin & Chart Configuration Reference ([`values.yaml`](values.yaml))

### Top-Level Lifecycle & Catalog Settings

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `bool` | `false` | Enables or disables the Vizier component in the XMC catalog. Defaults to `false` until explicitly enabled in profile values. |
| `acknowledgeExperimental` | `bool` | *(omitted)* | Explicit administrator acknowledgement required when `enabled: true` because `stability` is `EXPERIMENTAL`. |
| `namespace` | `string` | `""` (`.Release.Namespace`, or `"xmc"` when `"default"`/`""`) | Namespace where the `Component` CR is installed. |
| `deletionProtection` | `bool` | `true` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `string` | `"Delete"` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `list[string]` | `[]` | Optional list of prerequisite XMC Component names that must be healthy before Vizier is reconciled. |

### Connection Parameters (`parameters`)

All values under `spec.parameters` are rendered as strings in [`templates/component.yaml`](templates/component.yaml) to satisfy the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`):

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `parameters.endpoint` | `string` | `"vizier-service.xmc.svc.cluster.local:7077"` | Internal Kubernetes DNS endpoint (`host:port`) of the Open Source Vizier gRPC server. Published to consumers and, unless `healthCheckEndpoint` is set, probed via TCP by `xmc-bridge`. |
| `parameters.healthCheckEndpoint` | `string` | `""` (not rendered) | Optional `xmc-bridge` probe target. Set `"grpc://<host>:<port>"` to call `grpc.health.v1.Health/Check` for the overall server (empty service name) instead of the TCP probe. Never published to consumers. |

#### gRPC health probe (`grpc://`)

A TCP probe only proves that the port accepts connections. With `healthCheckEndpoint: "grpc://vizier-service.xmc.svc.cluster.local:7077"`, `xmc-bridge` calls the standard gRPC health service instead:

| Health/Check outcome | `vizier` readiness |
| :--- | :--- |
| `SERVING` | Ready, with the condition message `Static endpoint is reachable and healthy: gRPC health SERVING`. |
| `UNIMPLEMENTED` (the server does not register `grpc.health.v1`) | Ready, with the condition message `Static endpoint is reachable and healthy: gRPC health service not implemented; server reachable (TCP-equivalent fallback)`. The server answered over HTTP/2, which proves at least what the TCP probe proves. (gRPC clients also report a plain HTTP/2 `404` as `UNIMPLEMENTED`, so this does not prove the listener is Vizier.) |
| `NOT_SERVING`, `SERVICE_UNKNOWN`, any other gRPC error, or timeout | Not Ready (`Reconciling=True`) with the status in the message. |

- Upstream Open Source Vizier (`DefaultVizierServer`, including the dev fixture) does not register the health service, so it reports `UNIMPLEMENTED`. To get a real `SERVING` / `NOT_SERVING` signal, register a `grpc.health.v1` servicer in your Vizier server. What `SERVING` means (for example, whether it covers the Vizier database) is up to that servicer.
- The probe is plaintext (in-cluster). `grpcs://`, paths (named services), queries, and user info are rejected, and `vizier` stays not Ready with the parse error. To return to the TCP probe, leave `healthCheckEndpoint` empty or set it to `tcp://<host>:<port>`.
- If you restrict ingress to Vizier with a NetworkPolicy, keep admitting `app: xmc-bridge` pods on the Vizier port for the probe.

---

## 3. Configuration & Rendered Manifest Examples

> [!NOTE]
> Admins enable Vizier through the [`stable-2026b` profile](../../README.md) by setting `components.vizier.enabled=true` and `components.vizier.acknowledgeExperimental=true`.

### Example 1: Minimum Valid User Configuration

```yaml
components:
  vizier:
    enabled: true
    acknowledgeExperimental: true
```

Or via standalone CLI preview:

```bash
helm template vizier deploy/profiles/stable-2026b/components/vizier \
  --set enabled=true \
  --set acknowledgeExperimental=true
```

### Example 2: Rendered Kubernetes `Component` Manifest Output

```yaml
apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: "vizier"
  namespace: "xmc"
  labels:
    xmc.dev/profile: "stable-2026b"
    app.kubernetes.io/name: "vizier"
    app.kubernetes.io/managed-by: "Helm"
  annotations:
    argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true
spec:
  enabled: true
  componentType: COMPONENT_TYPE_CUSTOM_3P_WRAPPER
  componentClass: "optimization-engine"
  version: "0.1.0"
  chartVersion: "0.1.0"
  stability: "EXPERIMENTAL"
  acknowledgeExperimental: true
  deletionProtection: true
  deletionPolicy: "Delete"
  parameters:
    endpoint: "vizier-service.xmc.svc.cluster.local:7077"
```
