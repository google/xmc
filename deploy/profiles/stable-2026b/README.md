<!--
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# `stable-2026b` Component Profile

> [!WARNING]
> **Preview profile.** `stable-2026b` is not released yet (`status: preview` in [`profile.yaml`](profile.yaml)). Its catalog, values and charts may change incompatibly before release, and it is not covered by the GA lock that protects `stable-2026a`. dev_env installs or switches to it only with `--profile stable-2026b --allow-preview-profile`. At release, `status` flips to `stable` without renaming the profile.

This directory is the `stable-2026b` profile: a Helm meta-chart (`xmc-profile-stable-2026b`) that renders the `Component` custom resources (`components.xmc.dev/v1alpha1`) of its catalog from a single admin values file. It requires XMC `1.0.1+`, whose `xmc-bridge` selects embedded component charts by the `xmc.dev/profile` label of each `Component`.

The meta-chart works exactly like [`stable-2026a`](../stable-2026a/README.md): the same values contract, admin workflow (render, lint, install), teardown rules and rendering helpers apply. Replace `stable-2026a` with `stable-2026b` in the commands there.

> [!IMPORTANT]
> `stable-2026a` shipped with XMC `1.0.0` and is GA-locked: its chart files never change (a digest test enforces this). New or changed components go into a new profile such as this one.

## Catalog

| Component | Stability | Chart |
| :--- | :--- | :--- |
| `redpanda` | `STABLE` | Metadata-only; same as [`stable-2026a`](../stable-2026a/components/redpanda/README.md). |
| `clickhouse` | `STABLE` | Metadata-only; same as [`stable-2026a`](../stable-2026a/components/clickhouse/README.md). |
| `ingestion-gateway` | `STABLE` | Metadata-only; same as [`stable-2026a`](../stable-2026a/components/ingestion-gateway/README.md). |
| `vizier` | `EXPERIMENTAL` | [`components/vizier`](components/vizier/README.md): 3P wrapper for a bring-your-own Open Source Vizier endpoint. |
| `hpo` | `EXPERIMENTAL` | [`components/hpo`](components/hpo/README.md): `xmc-study-controller`, embedded in `xmc-bridge`. Requires `vizier` and `clickhouse` (and `dashboardServiceDeployment.enabled=true` for automatic `STUDY` parallel-coordinates chart provisioning). |

[`rules.yaml`](rules.yaml) sets `staticChecks.allowExperimental: true`, so the `EXPERIMENTAL` components are allowed but ship disabled, and enabling one requires `acknowledgeExperimental: true`. Its compatibility matrix also requires `vizier` and `clickhouse` for `hpo`. `hpo` additionally accepts `imageTag`, `parameters.maxTrialBudget` (a positive int32), and `parameters.studyControllerServiceAccount` (a Google service account email, or empty).

A metadata-only component directory holds only `Chart.yaml`, `values.yaml` and `templates/component.yaml`, which labels the CR `xmc.dev/profile: stable-2026b`. For a 1P component (today `ingestion-gateway`), `xmc-bridge` installs the `xmc-cmp-<name>` workload from the `stable-2026a` chart of the same name, because [`charts.go`](charts.go) embeds only the charts this profile changes; it fails the upgrade if that chart's version differs from the Component's `spec.chartVersion`. `redpanda` and `clickhouse` are 3P wrappers with no `xmc-cmp-<name>` release, so only their CR differs. A test keeps the name and version of each metadata-only 1P `Chart.yaml` equal to its `stable-2026a` counterpart; to change a component, ship and embed its full chart here instead.

## Selecting the Profile

- **dev_env:** pass `--profile stable-2026b --allow-preview-profile` to `dev_env.py gke-deploy` or `gke-upgrade` (see [`tools/dev_env/README.md`](../../../tools/dev_env/README.md)). Without `--allow-preview-profile`, dev_env refuses to install or switch to this preview profile before changing anything; once installed, it is kept (with a warning) without the flag. Without `--profile`, dev_env keeps the profile of the installed `xmc-profile` release (read from its chart name) and installs `stable-2026a` only when there is none, so switching always needs an explicit `--profile`.
- **Helm:** install or upgrade the `xmc-profile` release from this directory, for example `helm upgrade --install --reset-then-reuse-values xmc-profile deploy/profiles/stable-2026b -n xmc -f profile-values.yaml --wait`.

## Switching Between `stable-2026a` and `stable-2026b`

Both profiles use the same `xmc-profile` release and the same `Component` names, so switching is a `helm upgrade` of that release to the other chart:

- **`stable-2026a` → `stable-2026b`:** Helm relabels the existing CRs `xmc.dev/profile: stable-2026b`. `xmc-bridge` treats the label change as a change and re-applies each enabled 1P release once with the profile's chart; for the metadata-only components that is the same chart, so the workloads do not change.
- **`stable-2026b` → `stable-2026a`:** Helm deletes the CRs of components that `stable-2026a` does not ship (`vizier`, `hpo`). The `component-deletion-protection` admission policy denies that while they carry `deletionProtection: true`, and the `stable-2026a` schema rejects stored values for unknown components. `dev_env.py gke-upgrade --profile stable-2026a` handles both: it first fails, before changing anything, if a component that `stable-2026a` does not ship is still enabled; otherwise it clears `deletionProtection` on the disabled retired CRs owned by `xmc-profile` and strips their keys from the stored values before upgrading. Disable such components first. With plain Helm, follow the manual steps in [`docs/migrations/components.md`](../../../docs/migrations/components.md#after-migration-upgrading-xmc).

`--reset-then-reuse-values` carries only the values you supplied, so the chart default `global.platformProfile` follows the new chart. Do not set `global.platformProfile` in `profile-values.yaml`.
