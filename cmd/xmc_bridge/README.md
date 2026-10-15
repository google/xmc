# XMC Bridge Controller Daemon (`cmd/xmc_bridge`)

`xmc-bridge` is the deployable controller daemon responsible for reconciling Kubernetes `Component` custom resources (`xmc.dev/v1alpha1`) with the Component Manager Service (CMS) in XMC.

---

## Daemon Architecture

```mermaid
graph TD
    subgraph Kubernetes["Kubernetes Cluster"]
        CRD["Component CRD<br>(xmc.dev/v1alpha1)"]
        Deploy["xmc-bridge Deployment<br>(controller daemon)"]
        CM["ConfigMap<br>(xmc-bridge-config)"]
        RBAC["ClusterRole & Binding<br>(least-privilege RBAC)"]
        SA["ServiceAccount<br>(xmc-bridge)"]
    end

    Deploy -.->|"envFrom"| CM
    Deploy -->|"Watches & Reconciles"| CRD
    Deploy -->|"gRPC Client"| CMS["Component Manager Service (CMS)<br>(grpc-service:50051)"]
    Deploy -->|"Manages"| Workloads["1P Workloads & 3P CR Probing"]
```

---

## Component CRD Specification (`xmc.dev/v1alpha1`)

The `Component` Custom Resource Definition defines the declarative intent for platform components:

### Component Architecture Types

- **First-Party (1P) Native Components (`COMPONENT_TYPE_CUSTOM_1P_COMPONENT`)**:
  Microservices managed directly by XMC as Kubernetes Deployments and Services (e.g. Ingestion Gateway, Log Monitor, Actuators).
- **Third-Party (3P) Wrapped Operators (`COMPONENT_TYPE_CUSTOM_3P_WRAPPER`)**:
  Existing Kubernetes CRD operators and services (e.g. RayCluster, ClickHouseInstallation, Knative Service) whose lifecycle and network health coordinates are dynamically monitored and bound into CMS.

### Key Spec Fields

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `boolean` | (Required) | Desired activation state of the component. |
| `componentType` | `string` | `COMPONENT_TYPE_CUSTOM_1P_COMPONENT` | Control plane architecture model (`1P` vs `3P`). |
| `componentClass` | `string` | `""` | Optional classification identifier (e.g. core, storage, compute). |
| `deletionProtection` | `boolean` | `true` | Safeguards component inventory against accidental manifest deletion. |
| `deletionPolicy` | `string` | `Delete` | Teardown behavior upon deletion: `Delete` (prunes child workloads) or `Orphan` (retains child workloads). |
| `lifecycleControl` | `object` | `{}` | Lifecycle configuration such as `drainTimeoutSeconds` for graceful connection draining. |
| `parameters` | `map[string]string` | `{}` | Key-value configuration parameters passed into workload manifests (e.g. `image`, `port`, `chartPath`). |
| `wrapperRef` | `object` | `{}` | Target 3P CRD reference (`group`, `version`, `kind`, `name`, `namespace`) and custom JSONPath query paths. |

---

## Configuration & CLI Flags

Configuration can be supplied via environment variables (mounted from `xmc-bridge-config` ConfigMap in Helm) or CLI flags:

| Flag / Environment Variable | Default | Description |
| :--- | :--- | :--- |
| `--cms-address` / `CMS_ADDRESS` | `"localhost:50051"` | gRPC endpoint address of the Component Manager Service. |
| `--namespace` / `NAMESPACE` | `"xmc"` | Target Kubernetes namespace to watch and reconcile Component custom resources. |
| `--chart-path` / `HELM_CHART_PATH` | `""` | Base directory containing per-component Helm charts (`<base>/components/<name>`, `<base>/<name>`, `<base>/<name>.tgz`) or fallback umbrella chart path (defaults to embedded chart when empty). |
| `--probe-interval` / `PROBE_INTERVAL` | `5s` | Delay after a successful reconcile pass before the same Component is reconciled again, to observe workload health changes that emit no Component event. |
| `--probe-timeout` / `PROBE_TIMEOUT` | `2s` | Timeout of one Component's reconcile pass; persisting the pass's status is not bounded by it. |
| `--kubeconfig` / `KUBECONFIG` | `""` | Optional explicit path to kubeconfig file (defaults to in-cluster or `KUBECONFIG` env). |

On startup, `xmc-bridge` invokes `HydrateFromCluster` before entering the reconciliation loop to restore active 1P component states from the `xmc-component-values` ConfigMap, preventing redundant Helm release revision increments and ensuring post-restart deactivations execute cleanly.

### Reconciliation

`xmc-bridge` runs a controller-runtime manager whose cache watches the Component CRs of `--namespace` only. Reconciles and the dependents watch read Components from that cache, so the API server sees one list and watch per bridge rather than reads per Component. Each Component is reconciled on its own work queue item:

- when its spec (generation), labels or annotations change, or its deletion starts;
- when a Component it lists in `spec.dependencies` is created, deleted, changes generation, or becomes ready or unready as a dependency. Status writes that keep a dependency's readiness, such as condition timestamp refreshes, do not re-enqueue its dependents;
- `--probe-interval` after each successful pass;
- after a failed pass (CMS, actuation or status persistence error), with per-Component exponential backoff from 5s to 300s and ±10% jitter. A successful pass resets the backoff.

A pass always persists its status, also when it fails, so the CR reports the failure while the retry is pending.

---

## Helm Integration & Deployment

The component bridge controller is feature-flagged under `componentsDeployment` in Helm values:

```yaml
componentsDeployment:
  enabled: false                                  # Set to true to deploy xmc-bridge controller
  cmsAddress: "grpc-service:50051"                # In-cluster gRPC endpoint for ESS/CMS
  chartPath: ""                                   # Optional base Helm chart directory or fallback chart path
  probeInterval: "5s"                             # Per-Component re-probe delay after a successful pass
  probeTimeout: "2s"                              # Timeout per Component reconcile pass

xmc_bridge_custom_image: ""                       # Optional custom container image override
```

To install or upgrade:

```bash
helm upgrade --install xmc deploy/helm \
  --set componentsDeployment.enabled=true \
  --set componentsDeployment.cmsAddress=grpc-service:50051
```

### CRD lifecycle

The `components.xmc.dev` CRD is rendered whether or not `componentsDeployment.enabled` is set, because deleting a CRD deletes every CR of that type. It carries:

- `helm.sh/resource-policy: keep`, so `helm uninstall` and a flag flip leave the CRD and all Component CRs in place.
- `argocd.argoproj.io/sync-options: Prune=false,Delete=false`, so Argo CD never prunes or deletes it.
- `argocd.argoproj.io/sync-wave: "-1"`, which orders it before the Component CRs only when both are synced by the same Argo CD Application. Across Applications it has no effect: sync the Application that installs this chart first, or give the Application holding the CRs a sync retry policy.

The `stable-2026a` profile's Component CRs carry `argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true`, so an Argo CD application's first sync does not fail its dry run before the CRD exists. Set the same annotation on any Component CR you author yourself.

Because the CRD renders even with `componentsDeployment.enabled=false`, whoever installs or upgrades the chart needs permission to create and update `customresourcedefinitions`.

The `component-deletion-protection` `ValidatingAdmissionPolicy` and its binding follow the CRD: they render whatever `componentsDeployment.enabled` is set to and carry `helm.sh/resource-policy: keep`, so disabling the bridge or uninstalling the chart never silently drops deletion protection for the CRs the kept CRD still holds. The installer therefore also needs permission to manage `validatingadmissionpolicies` and `validatingadmissionpolicybindings`. The policy exempts only the Kubernetes namespace controller and garbage collector, and `system:masters`; there is no `xmc-platform-admins` breakglass group. To delete a protected Component, set `spec.deletionProtection: false` first. A kept policy and binding also retain their release annotations, so reassign them the same way as the CRD below before installing under a different release.

The kept CRD retains the `meta.helm.sh/release-name` and `meta.helm.sh/release-namespace` annotations of the release that created it. Reinstalling under the same release name and namespace adopts it again. Installing under a different release name or namespace fails with an ownership error; reassign the CRD first:

```bash
kubectl annotate crd components.xmc.dev --overwrite \
  meta.helm.sh/release-name=<release> meta.helm.sh/release-namespace=<namespace>
```

With `componentsDeployment.enabled=false`, nothing removes the `finalizers.xmc.dev/deletion-protection` finalizer that `xmc-bridge` attached, so deleting a Component CR, or the namespace that holds it, hangs. Re-enable the bridge, or strip the finalizer by hand. Stripping it skips the bridge's teardown of the component's workload release:

```bash
kubectl patch components.xmc.dev <name> -n <namespace> --type=merge -p '{"metadata":{"finalizers":null}}'
```

Removing the CRD is a deliberate manual step. While `xmc-bridge` is still running, so it can release each CR's finalizer, delete every Component CR (see the profile's [Disable or Uninstall](../../deploy/profiles/stable-2026a/README.md#disable-or-uninstall) flow). Then run `kubectl delete crd components.xmc.dev`, and remove the kept policy with `kubectl delete validatingadmissionpolicybinding component-deletion-protection-binding` and `kubectl delete validatingadmissionpolicy component-deletion-protection`.

---

## Security & RBAC Least Privilege

- **ServiceAccount**: `xmc-bridge` runs under a dedicated service account with `automountServiceAccountToken: true`.
- **ClusterRole**: Grants least-privilege permissions:
  - Full CRUD permissions on `components.xmc.dev` (including `/status` and `/finalizers`).
  - Workload management permissions on `apps/deployments` and `core/services` for 1P components.
  - Read-only (`get`, `list`, `watch`) discovery permissions across all API groups (`*`) to support dynamic 3P CR status detection.
  - Security contexts enforce read-only root filesystems, non-root execution, privilege escalation disabled, and dropping all capabilities (`ALL`).
