# Ingestion Gateway 1P Native Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/ingestion-gateway/`) defines the XMC 1P Native Component blueprint ([`templates/component.yaml`](templates/component.yaml)) and consolidated Kubernetes workload templates ([`templates/deployment.yaml`](templates/deployment.yaml) rendering `Deployment`, `Service`, `HorizontalPodAutoscaler`, and standalone `Role`/`RoleBinding`) for the **Ingestion Gateway** metrics service.

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the Ingestion Gateway blueprint defined in [`templates/component.yaml`](templates/component.yaml):

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"ingestion-gateway"` | Canonical catalog identifier managed by the XMC GitOps Bridge Controller (`xmc-bridge`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["xmc.dev/component-type"]` | `"custom-1p-component"` | Identifies the blueprint as an XMC first-party (1P) managed workload. |
| `metadata.labels["app.kubernetes.io/name"]` | `"ingestion-gateway"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_1P_COMPONENT` | Hardcoded as a 1P Native Component actuated via Helm by `xmc-bridge`. |
| `spec.componentClass` | `"metrics-ingestion"` | Canonical service class for the experiment metrics ingestion pipeline. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |
| `spec.dependencies` | `["redpanda", "clickhouse"]` | Declares upstream dependencies on the `redpanda` and `clickhouse` 3P Wrapper Components: the gateway publishes to Redpanda, and without ClickHouse the metrics have no store. `xmc-bridge` waits for both to be Ready before actuating, and CMS blocks disabling either while the gateway is enabled. |

---

## 2. Configuration & Parameter Mapping ([`values.yaml`](values.yaml))

All entries in `spec.parameters` are serialized as quoted strings (`| toString | quote`) to comply with the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`) and `component-linter` validation:

| Parameter | Default | Target Flag / Spec Field | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `false` | `spec.enabled` | Enables or disables the `ingestion-gateway` Component CR in the catalog. |
| `deployWorkload` | `false` | Template guard | When `true` (or when `ingestionGatewayDeployment.enabled=true` via `xmc-bridge`), renders Kubernetes workload resources (`Deployment`, `Service`, `HPA`, `RBAC`). |
| `global.imageTag` | `""` | `spec.imageTag` & Container `image` | Overrides the container image tag across both the `Component` CR (`spec.imageTag`) and the `Deployment` container spec (`{{ repository }}:{{ global.imageTag }}`). |
| `parameters.port` | `8080` (`"8080"`) | `-port`, `containerPort`, `Service.spec.ports` | HTTP/gRPC port exposed by the Ingestion Gateway container and ClusterIP Service. |
| `parameters.redpandaBrokers` | `"redpanda-0.redpanda.xmc.svc.cluster.local:9092"` | `-redpanda_brokers` | Static fallback Kafka broker addresses when dynamic discovery from `Component/redpanda` is inactive. |
| `parameters.topicName` | `"xmc-metrics"` | `-topic_name` | Default Kafka topic for ingested telemetry and experiment metrics. |
| `parameters.minPartitions` | `128` (`"128"`) | `-min_partitions` | Minimum partition count when auto-creating the Kafka topic. |
| `parameters.minReplicas` | `1` (`"1"`) | `-min_replicas` | Minimum replication factor for the Kafka topic (matches the umbrella chart default). |

---

## 3. Actuation Lifecycle

1. **Declare intent**: install the [`stable-2026a` profile](../../README.md) with `components.ingestion-gateway.enabled=true`. The profile renders only the `Component/ingestion-gateway` CR from [`templates/component.yaml`](templates/component.yaml); this chart's workload templates are never rendered by the profile.
2. **Actuate**: `xmc-bridge` reconciles the CR and installs the `xmc-cmp-ingestion-gateway` release from this chart with `ingestionGatewayDeployment.enabled=true`. The release renders the `Deployment`, `Service`, `HPA`, the `ingestion-gateway-components-reader` `Role`/`RoleBinding`, and the `ingestion-gateway-network-policy` `NetworkPolicy`. The policy's rules mirror the umbrella policy used when `enable_components=false`.
3. **Deactivate**: setting `enabled=false` makes `xmc-bridge` uninstall `xmc-cmp-ingestion-gateway`. This removes the workload together with its RBAC and NetworkPolicy.

The `xmc-cmp-ingestion-gateway` release self-provisions its reader RBAC so that the `RoleBinding` subject always matches the `Deployment` `serviceAccountName`. [`templates/component.yaml`](templates/component.yaml) is skipped for `xmc-cmp-*` releases because the `Component` CR is owned by the user's release, not by `xmc-bridge`.

---

## 4. Coexistence with the Umbrella Chart (`deploy/helm/`)

| `enable_components` (`componentsDeployment.enabled`) | Gateway workload owner |
| :---: | :--- |
| `false` | Umbrella chart, when `enable_metrics` (`ingestionGatewayDeployment.enabled`) is `true` (unchanged legacy behavior). |
| `true` | Only this chart: the gateway is optional and runs once the profile is installed with `components.ingestion-gateway.enabled=true` and `redpanda` and `clickhouse` are Ready. The umbrella renders no gateway Deployment/Service/HPA/RBAC/NetworkPolicy. |

The gateway `NetworkPolicy` follows the same ownership. With `enable_components=true`, it ships in the `xmc-cmp-ingestion-gateway` release regardless of `enable_metrics`. A test keeps its rules identical to the umbrella policy.
