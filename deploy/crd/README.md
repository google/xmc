# XMC Custom Resource Definitions & Admission Policies (`deploy/crd`)

This directory contains standalone, pure Kubernetes Custom Resource Definition (CRD) schemas and `ValidatingAdmissionPolicy` manifests for XManager on Cloud (XMC).

---

## 1. Directory Purpose & Scope

The YAML manifests in `deploy/crd/` are **pure static manifests** without Go template directives:
- **Local Development & Tooling**: Used for manual `kubectl apply`, schema validation with `kubectl --dry-run=client`, and static manifest linters.
- **Controller Unit & Integration Testing**: Consumed directly by test frameworks such as `envtest` (Kubebuilder / controller-runtime) without requiring a full Helm templating engine.
- **Reference Schemas**: Serves as the authoritative, static declarative schema contract for external tooling and clients.

> [!NOTE]
> Manifests in `deploy/crd/` are **not deployed automatically** to production clusters by Cluster Toolkit (CTK v2) or Helm.

---

## 2. Production Deployment & Feature Flag Guarding

For live cluster deployments (via Cluster Toolkit CTK v2 and Helm), production resources are packaged under [`deploy/helm/templates/components/`](../helm/templates/components/):

- **Conditional Guarding**: In Helm, component CRDs and admission policies are placed in `templates/components/` (rather than Helm's un-templated `crds/` folder) so they are conditionally managed by the `enable_components` toggle.
- **Cluster Toolkit Propagation**:
  - Setting `enable_components: true` in your CTK deployment configuration (e.g. `xmanager-cloud-deployment.yaml`) maps to `componentsDeployment.enabled: true` in Helm.
  - When `enable_components: false` (default), Helm completely suppresses the creation of component CRDs, ValidatingAdmissionPolicies, and bindings.

```mermaid
flowchart LR
    CTK["CTK Deployment Config\n(enable_components: true/false)"] -->|vars.enable_components| HELM["Helm Chart (deploy/helm)\ncomponentsDeployment.enabled"]
    HELM -->|true| APPLIED["Apply deploy/helm/templates/components/*"]
    HELM -->|false (default)| SKIPPED["Skip CRDs & Manifests"]
```

---

## 3. Manifest Inventory

| Manifest | Kind / API Version | Description |
| :--- | :--- | :--- |
| [`component_crd.yaml`](component_crd.yaml) | `CustomResourceDefinition` (`apiextensions.k8s.io/v1`) | Defines `Component.xmc.dev/v1alpha1` with OpenAPI v3 schemas, status subresource, and embedded CEL validations (`x-kubernetes-validations`). |
| [`component_deletion_vap.yaml`](component_deletion_vap.yaml) | `ValidatingAdmissionPolicy` & `Binding` (`admissionregistration.k8s.io/v1`) | Validates and blocks `DELETE` operations on `Component` custom resources when `deletionProtection` is enabled. |
