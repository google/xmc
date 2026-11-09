# Component Migration Side Script (`tools/migrations/components/`)

One-time operator CLI (`migrate.py`) for migrating pre-`1.0.0` XMC installations from legacy umbrella metrics workloads (`componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`xmc-profile`).

> [!NOTE]
> **Supported Versions & Execution Guardrails**: Valid from any `< 1.0.0` legacy umbrella installation (`componentsDeployment.enabled=false`, e.g. `<= 0.18.x`) to XMC **`0.18.0` – `0.18.x` or `1.x` (`1.0.0+`)**. Targets `<= 0.17.0` are unsupported. Every subcommand validates local codebase and live cluster version bounds and requires an interactive TTY `yes` confirmation before running.

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
