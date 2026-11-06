# HPO 1P Native Component Blueprint (`stable-2026a`)

This Helm sub-chart (`deploy/profiles/stable-2026a/components/hpo/`) defines the XMC 1P Native Component blueprint ([`templates/component.yaml`](templates/component.yaml)) for **Hyperparameter Optimization (HPO)** (`hyperparameter-optimization`) and the `xmc-study-controller` workload that `xmc-bridge` actuates for it ([`templates/deployment.yaml`](templates/deployment.yaml), [`templates/serviceaccount.yaml`](templates/serviceaccount.yaml)).

> [!NOTE]
> The `xmc-study-controller` workload is a `Deployment` and a plain `ServiceAccount` only. It serves health and metrics endpoints and does not yet run Study reconciliation. The chart renders no `Service`, `Role`/`RoleBinding`, `NetworkPolicy` or `ConfigMap`, and the `ServiceAccount` carries no Workload Identity annotation.

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

## 2. Configuration & Value Mapping ([`values.yaml`](values.yaml))

| Value | Default | Target Spec Field | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `false` | `spec.enabled` | Enables or disables the `hpo` Component CR in the catalog. |
| `acknowledgeExperimental` | *(omitted)* | `spec.acknowledgeExperimental` | Explicit administrator acknowledgement required when `enabled: true` because `stability` is `EXPERIMENTAL`. |
| `namespace` | `""` (`.Release.Namespace`, or `"xmc"` when `"default"`/`""`) | `Component.metadata.namespace` | Target Kubernetes namespace for the `Component` CR and actuated workload resources. |
| `imageTag` | `""` | `spec.imageTag` | `xmc-study-controller` image tag, used when `global.imageTag` is empty. Omitted from the CR when both are empty. |
| `deletionProtection` | `true` | `spec.deletionProtection` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `"Delete"` | `spec.deletionPolicy` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `["vizier", "clickhouse"]` | `spec.dependencies` | Upstream XMC Component names required before `hpo` is actuated. |

---

## 3. Actuation Lifecycle

1. **Declare intent**: install the [`stable-2026a` profile](../../README.md) with `components.vizier.enabled=true`, `components.vizier.acknowledgeExperimental=true`, `components.clickhouse.enabled=true`, `components.hpo.enabled=true`, and `components.hpo.acknowledgeExperimental=true`.
2. **Actuate**: once `vizier` and `clickhouse` report `Ready=True`, `xmc-bridge` installs the `xmc-cmp-hpo` release from the embedded chart (`deploy/profiles/stable-2026a/charts.go`). [`templates/component.yaml`](templates/component.yaml) is guarded by `{{- if not (hasPrefix "xmc-cmp-" .Release.Name) -}}` so the `xmc-cmp-hpo` release never re-renders the `Component` CR, and the workload templates are guarded by `{{- if hasPrefix "xmc-cmp-" .Release.Name }}` so the profile release never renders them. While a dependency is not Ready, `hpo` reports `Ready=False` with reason `DependenciesNotReady` and no `xmc-cmp-hpo` release exists.
3. **Deactivate**: setting `components.hpo.enabled=false` makes `xmc-bridge` uninstall `xmc-cmp-hpo`.

---

## 4. `xmc-study-controller` Workload

Rendered only for the `xmc-cmp-hpo` release:

| Object | Name | Notes |
| :--- | :--- | :--- |
| `Deployment` | `xmc-study-controller` | 1 replica of [`cmd/study_controller`](../../../../../cmd/study_controller). Serves `/healthz` (liveness), `/readyz` (readiness) and `/metrics` (Prometheus) on HTTP port `8080`. Runs as non-root with a read-only root filesystem and no service account token. |
| `ServiceAccount` | `xmc-study-controller` | Pod identity, without a Workload Identity annotation. |

The pod carries the label `xmc.google.com/vizier-client: "true"`, which admits it through the [`vizier`](../vizier/README.md) NetworkPolicy.

`xmc-bridge` may only manage the objects named above; their names are allowlisted in the bridge Role ([`deploy/helm/templates/components/rbac.yaml`](../../../../helm/templates/components/rbac.yaml)), so renaming or adding an object requires updating that Role.

### Image Resolution

The container image is resolved by [`templates/_helpers.tpl`](templates/_helpers.tpl):

1. `hpoDeployment.image`, the image `xmc-bridge` resolves for the release. It is used verbatim when it has a tag or digest; otherwise the tag below is appended.
2. Otherwise `us-docker.pkg.dev/xmc-prod/xmc/study-controller` with `global.imageTag`, else `imageTag`, else `latest`.

`xmc-bridge` resolves `hpoDeployment.image` from the `DEFAULT_HPO_IMAGE` environment variable, set from the umbrella chart value `hpo_custom_image` (the Cluster Toolkit blueprint sets it from `study_controller_image`, `<image_registry>/study-controller-<environment>:<image_tag>`). Without `DEFAULT_HPO_IMAGE`, an explicit `spec.imageTag` resolves to the same default repository, `us-docker.pkg.dev/xmc-prod/xmc/study-controller:<imageTag>`. The image is built by `//cmd/study_controller:study_controller_image`.
