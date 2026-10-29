# Component Migration Side Script (`tools/migrations/components/`)

One-time operator CLI (`migrate.py`) for migrating existing XMC installations from legacy umbrella metrics workloads (`componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`componentsDeployment.enabled=true`).

> [!NOTE]
> **Supported Versions**: Valid from any legacy umbrella installation (`componentsDeployment.enabled=false`, e.g. `<= 0.16.x` or `0.17.0`–`0.18.x` prior to enabling components) to XMC **`0.17.0` – `0.18.x`**. Targets `<= 0.16.0` are unsupported (missing `stable-2026a` gateway parameter/schema parity). This directory is temporary migration tooling for the `0.17.0`–`0.18.x` transition window and is slated for removal in `0.19.0` once existing installations have completed cutover.

## Subcommands

```bash
# 1. Generate and schema-validate profile-values.yaml from live umbrella Helm values
./tools/migrations/components/migrate.py prepare -n xmc -r xmc -o profile-values.yaml

# 2. Transfer live ingestion-gateway Helm ownership annotations before enabling components
./tools/migrations/components/migrate.py handoff -n xmc -r xmc

# Undo Step 2 (or restore legacy ownership after an Orphan profile uninstall)
./tools/migrations/components/migrate.py handoff --undo -n xmc -r xmc

# 3. Wait for Component readiness, strip temporary keep annotations, and retire legacy NetworkPolicy
./tools/migrations/components/migrate.py finalize -n xmc -r xmc
```

For the complete step-by-step operator sequence and rollback procedure, see [`docs/migrations/components.md`](../../../docs/migrations/components.md).

## Testing

```bash
bazel test //tools/migrations/components:migrate_test
```
