# Component Migration Side Script (`tools/migrations/components/`)

One-time operator CLI (`migrate.py`) for migrating existing XMC installations from legacy umbrella metrics workloads (`componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`componentsDeployment.enabled=true`).

> [!NOTE]
> This directory is temporary migration tooling for the Release `N` legacy-to-components transition and is slated for removal in Release `N+1` once existing installations have completed cutover.

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
