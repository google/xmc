# `stable-2026a` Component Profile

This directory is the `stable-2026a` profile: a Helm meta-chart (`xmc-profile-stable-2026a`) that renders the `Component` custom resources (`components.xmc.dev/v1alpha1`) for every catalog component in the profile from a single admin values file.

The meta-chart has no Helm `dependencies:`. It reads each co-located component chart under [`components/`](components/) through `.Files` and renders only that chart's `templates/component.yaml`. Component workloads are installed separately by `xmc-bridge` as `xmc-cmp-<name>` releases from the same component charts.

## Layout

| Path | Purpose |
| :--- | :--- |
| [`Chart.yaml`](Chart.yaml) | Meta-chart metadata. The `xmc.dev/profile` annotation carries the profile name. |
| [`values.yaml`](values.yaml) | Admin values: `global` settings and `components.<name>` overrides. All components are disabled by default. |
| [`values.schema.json`](values.schema.json) | Strict schema for admin values. Unknown components and keys are rejected. |
| [`templates/components.yaml`](templates/components.yaml) | Emits one `Component` CR per catalog component, in dependency order. |
| [`templates/_helpers.tpl`](templates/_helpers.tpl) | Value merging and `component.yaml` rendering helpers. |
| [`profile.yaml`](profile.yaml) | Profile metadata: `name`, `minXmcVersion`, `supportWindow`. |
| [`rules.yaml`](rules.yaml) | Static checks and compatibility matrix used by the [component linter](../../../cmd/component-linter/README.md). |
| [`components/`](components/) | Component charts: [`redpanda`](components/redpanda/README.md), [`clickhouse`](components/clickhouse/README.md), [`ingestion-gateway`](components/ingestion-gateway/README.md). |
| [`charts.go`](charts.go) | Embeds the `ingestion-gateway` chart into `xmc-bridge`. |

## Admin Workflow

### Prerequisites

The XMC umbrella chart must be installed with `enable_components=true` (Helm value `componentsDeployment.enabled=true`). That installs the `Component` CRD, the `component-deletion-protection` admission policy, and `xmc-bridge`.

Components are an unlaunched feature, so install the profile fresh. If you installed a component chart directly as its own Helm release during development, uninstall that release first. There is no procedure for adopting existing releases into the profile.

### Install

Install the profile as a single release:

```bash
# 1. Render the Component CRs with your overrides.
mkdir -p rendered
helm template xmc-profile deploy/profiles/stable-2026a -n xmc -f my-values.yaml > rendered/profile.yaml

# 2. Validate dependencies, schema, and the compatibility matrix offline.
go run ./cmd/component-linter \
  --manifests-dir=rendered \
  --rules-config=deploy/profiles/stable-2026a/rules.yaml

# 3. Install the same values, or commit rendered/ to your GitOps repository instead.
helm install xmc-profile deploy/profiles/stable-2026a -n xmc -f my-values.yaml
```

The release name must not start with `xmc-cmp-`, which is reserved for `xmc-bridge` component releases; rendering fails if it does.

To isolate a template error in one component, render that component chart on its own, for example `helm template redpanda deploy/profiles/stable-2026a/components/redpanda`.

Example `my-values.yaml` enabling the metrics pipeline:

```yaml
components:
  redpanda:
    enabled: true
  clickhouse:
    enabled: true
    parameters:
      storageSize: "50Gi"
  ingestion-gateway:
    enabled: true
```

### Disable or Uninstall

`Component` CRs default to `deletionProtection: true`, and the `component-deletion-protection` admission policy denies deleting a `Component` unless its `spec.deletionProtection` is `false`.

- **Disable a component:** set `components.<name>.enabled=false` and run `helm upgrade`. The CR stays, and `xmc-bridge` removes the `xmc-cmp-<name>` workload release.
- **Uninstall the profile:** first run `helm upgrade` with `components.<name>.deletionProtection=false` for every component, then run `helm uninstall xmc-profile -n xmc`.
- **Recover a stuck uninstall:** if the admission policy denied the uninstall and the release is stuck in `uninstalling`, patch each remaining CR, for example `kubectl patch components.xmc.dev <name> -n xmc --type=merge -p '{"spec":{"deletionProtection":false}}'`, then run `helm uninstall` again.
- **Remove a component from the catalog:** first ship a profile patch that sets the component to `enabled: false` and `deletionProtection: false`. Otherwise the admission policy denies the delete that the next `helm upgrade` performs.

## Values Contract

Per-component overrides live under `components.<name>` and are deep-merged over that component's `values.yaml`. Explicit `false`, `0`, and `""` overrides are honored, and lists (such as `dependencies`) replace the defaults. `null` overrides are not supported: the schema rejects most of them, and Helm drops the rest so the component default applies. Use `[]` or an explicit value instead.

| Key | Components | Description |
| :--- | :--- | :--- |
| `global.platformProfile` | all | Fixed to `stable-2026a`. |
| `global.imageTag` | `ingestion-gateway` | Image tag rendered into `spec.imageTag`. |
| `enabled` | all | Renders `spec.enabled`. |
| `namespace` | all | Namespace of the `Component` CR. Must be a DNS-1123 label. |
| `deletionProtection` | all | Renders `spec.deletionProtection`. |
| `deletionPolicy` | all | `Delete` or `Orphan`. |
| `parameters` | all | Component parameters. `redpanda` and `clickhouse` accept only the parameters documented in their READMEs; `ingestion-gateway` passes extra keys through. |
| `dependencies` | all | Component names rendered into `spec.dependencies`. |
| `imageTag` | `ingestion-gateway` | Used when `global.imageTag` is empty. |

These keys are rejected by the schema:

- `componentClass` and chart or component versions, because the catalog fixes component identity. Each component's `components/<name>/Chart.yaml` is the only version pin.
- Workload keys such as `deployWorkload`, because the profile never renders workloads.

## Runtime Dependencies

Component runtime dependencies are not Helm dependencies. They are expressed in two places:

1. `spec.dependencies` on each `Component` CR, from the component's `values.yaml` (for example, `ingestion-gateway` depends on `redpanda` and `clickhouse`). `xmc-bridge` waits for dependencies to be Ready before actuating a component.
2. The `compatibilityMatrix` in [`rules.yaml`](rules.yaml), which the component linter checks against the rendered CRs.

## Versioning

- Each component's `components/<name>/Chart.yaml` `version` is rendered into `spec.version` and `spec.chartVersion`.
- A profile patch release bumps the changed component's `Chart.yaml` version and the patch version in the profile [`Chart.yaml`](Chart.yaml).
- [`profile.yaml`](profile.yaml) records the minimum XMC release and the support window. A test keeps its `name` consistent with the `Chart.yaml` annotation, `values.yaml`, and `values.schema.json`.

## Authoring `component.yaml`

The meta-chart renders each `components/<name>/templates/component.yaml` with `tpl`, so these templates must follow this invariant:

- Use only `.Values`, `.Chart` (`Name`, `Version`, `AppVersion`), `.Release`, `.Capabilities`, and `.Template`.
- Do not `include` named templates from the component chart's `_*.tpl` files, and do not use `.Files`. Neither is available when the profile renders the template.

When adding a component, add it to the list in [`templates/components.yaml`](templates/components.yaml), to `components` in [`values.yaml`](values.yaml) and [`values.schema.json`](values.schema.json), and to the `:chart` filegroup in [`BUILD.bazel`](BUILD.bazel). [`profile_chart_test.go`](../../../test/helm/profiles/stable2026a/profile_chart_test.go) checks that rendering matches each standalone component chart and that the catalog lists stay consistent.
