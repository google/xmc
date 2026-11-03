# HPO 1P Native Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/hpo/`) defines the XMC 1P Native Component blueprint ([`templates/component.yaml`](templates/component.yaml)) for **Hyperparameter Optimization (HPO)** (`hyperparameter-optimization`).

> [!NOTE]
> In HPO-01, this chart ships the `Component/hpo` Custom Resource blueprint (`templates/component.yaml`). The Kubernetes workload manifests for `xmc-study-controller` (`Deployment`, `Service`, `ServiceAccount`, `Role`/`RoleBinding`, and `NetworkPolicy`) are added in HPO-05.

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the HPO blueprint defined in [`templates/component.yaml`](templates/component.yaml):

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"hpo"` | Canonical catalog identifier managed by the XMC GitOps Bridge Controller (`xmc-bridge`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026a"` | Binds this component definition to the `stable-2026a` release profile. |
| `metadata.labels["xmc.dev/component-type"]` | `"custom-1p-component"` | Identifies the blueprint as an XMC first-party (1P) managed workload. |
| `metadata.labels["app.kubernetes.io/name"]` | `"hpo"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_1P_COMPONENT` | Hardcoded as a 1P Native Component actuated via Helm by `xmc-bridge`. |
| `spec.componentClass` | `"hyperparameter-optimization"` | Canonical service class for hyperparameter optimization studies and trial orchestration. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |
| `spec.stability` | `"EXPERIMENTAL"` | Catalog-declared maturity stage; enabling requires `acknowledgeExperimental: true`. |
| `spec.dependencies` | `["vizier", "clickhouse"]` | Declares upstream dependencies on the [`vizier`](../vizier/README.md) optimization engine and [`clickhouse`](../clickhouse/README.md) metrics store. `xmc-bridge` waits for both to be Ready before actuating `hpo`. |

---

## 2. Configuration & Parameter Mapping ([`values.yaml`](values.yaml))

All entries in `spec.parameters` are serialized as quoted strings (`| toString | quote`) to comply with the `components.xmc.dev` CRD schema (`additionalProperties: { type: string }`) and `component-linter` validation:

| Parameter | Default | Target Spec Field | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `false` | `spec.enabled` | Enables or disables the `hpo` Component CR in the catalog. |
| `acknowledgeExperimental` | *(omitted)* | `spec.acknowledgeExperimental` | Explicit administrator acknowledgement required when `enabled: true` because `stability` is `EXPERIMENTAL`. |
| `namespace` | `""` (`.Release.Namespace`, or `"xmc"` when `"default"`/`""`) | `Component.metadata.namespace` | Target Kubernetes namespace for the `Component` CR and actuated workload resources. |
| `global.imageTag` | `""` | `spec.imageTag` | Profile-wide container image tag override; takes precedence over per-component `imageTag` when non-empty. |
| `imageTag` | `"latest"` | `spec.imageTag` | Per-component fallback container image tag used when `global.imageTag` is empty. |
| `deletionProtection` | `true` | `spec.deletionProtection` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `"Delete"` | `spec.deletionPolicy` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `["vizier", "clickhouse"]` | `spec.dependencies` | Upstream XMC Component names required before `hpo` is actuated. |
| `parameters.maxTrialBudget` | `"1000"` | `spec.parameters.maxTrialBudget` | Upper ceiling on `StudyConfig.max_trials`; study budgets exceeding this cap are clamped with a warning. |
| `parameters.maxParallelTrials` | `"100"` | `spec.parameters.maxParallelTrials` | Upper ceiling on `StudyConfig.max_parallel_trials`; parallel trial counts exceeding this cap are clamped with a warning. |

---

## 3. Actuation Lifecycle

1. **Declare intent**: install the [`stable-2026a` profile](../../README.md) with `components.vizier.enabled=true`, `components.vizier.acknowledgeExperimental=true`, `components.clickhouse.enabled=true`, `components.hpo.enabled=true`, and `components.hpo.acknowledgeExperimental=true`.
2. **Actuate**: once `vizier` and `clickhouse` report `Ready=True`, `xmc-bridge` installs the `xmc-cmp-hpo` release from the embedded chart (`deploy/profiles/stable-2026a/charts.go`). [`templates/component.yaml`](templates/component.yaml) is guarded by `{{- if not (hasPrefix "xmc-cmp-" .Release.Name) -}}` so the `xmc-cmp-hpo` release never re-renders the `Component` CR.
3. **Deactivate**: setting `components.hpo.enabled=false` makes `xmc-bridge` uninstall `xmc-cmp-hpo`.
