# Component Platform Helm Templates (`deploy/helm/templates/components`)

This directory contains the Helm templates and Kubernetes manifests for the XMC Components platform, providing declarative GitOps infrastructure for managing first-party (1P) and third-party (3P) catalog components on Kubernetes.

---

## Architecture Overview

```mermaid
graph TD
    subgraph Helm["Helm Deployment (deploy/helm)"]
        CRD["Component CRD<br>(xmc.dev/v1alpha1)"]
        Deploy["xmc-bridge Deployment<br>(controller daemon)"]
        RBAC["ClusterRole & Binding<br>(least-privilege RBAC)"]
        SA["ServiceAccount<br>(xmc-bridge)"]
    end

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
| `parameters` | `map[string]string` | `{}` | Key-value configuration parameters passed into workload manifests (e.g. `image`, `port`). |
| `wrapperRef` | `object` | `{}` | Target 3P CRD reference (`group`, `version`, `kind`, `name`, `namespace`) and custom JSONPath query paths. |

---

## Helm Configuration Values

The component bridge controller is feature-flagged under `componentsDeployment`:

```yaml
componentsDeployment:
  enabled: false                                  # Set to true to deploy xmc-bridge controller
  cmsAddress: "grpc-service:50051"                # In-cluster gRPC endpoint for ESS/CMS
  probeInterval: "5s"                             # Namespace reconciliation sweep interval
  probeTimeout: "2s"                              # Timeout per component reconciliation pass

xmc_bridge_custom_image: ""                       # Optional custom container image override
```

---

## Security & RBAC Least Privilege

- **ServiceAccount**: `xmc-bridge` runs under a dedicated service account with `automountServiceAccountToken: true`.
- **ClusterRole**: Grants least-privilege permissions:
  - Full CRUD permissions on `components.xmc.dev` (including `/status` and `/finalizers`).
  - Workload management permissions on `apps/deployments` and `core/services` for 1P components.
  - Read-only (`get`, `list`, `watch`) discovery permissions across all API groups (`*`) to support dynamic 3P CR status detection.
  - Security contexts enforce read-only root filesystems, non-root execution, privilege escalation disabled, and dropping all capabilities (`ALL`).
