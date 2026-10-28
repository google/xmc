# PostgreSQL addon (`deploy/addons/postgres`)

A single-replica PostgreSQL 16 for running XMC **without Cloud Spanner** on any Kubernetes
cluster (KinD, EKS, on-prem). It is meant for development and evaluation: one replica, one
PersistentVolumeClaim, no backups. Use a managed PostgreSQL service for production and point
the chart at it with the same `database.postgres.*` values.

## What it deploys

| Object | Purpose |
| :--- | :--- |
| Secret `xmc-postgres-credentials` (`username`, `password`) | Created by the script with a random password; read by the database Pod, the migration Jobs, and the chart (`database.postgres.passwordSecret`). |
| ConfigMap `postgres-init` | Creates the `ess` and `xid` databases on first start. |
| PVC `postgres-data`, Deployment `postgres`, Service `postgres` | The database, reachable at `postgres.<namespace>.svc.cluster.local:5432`. |
| Jobs `postgres-migrate-ess-*`, `postgres-migrate-xid-*` | Apply the schema migrations from `internal/storage/postgres/migrations` with the `postgres-migrator` image. |

## Usage

```bash
# Build and push the migrator image (see INSTALL.md for XMC_ACR_TARGET / XMC_ENV / BUILD_TAG).
bazel run //internal/storage/postgres/migrations:postgres_migrator_image_push

MIGRATOR_IMAGE="${XMC_ACR_TARGET}/postgres-migrator-${XMC_ENV}:${BUILD_TAG}" \
  deploy/addons/postgres/setup_postgres_addon.sh
```

The script prints the Helm values to use; in short:

```yaml
database:
  backend: postgres
  postgres:
    host: postgres.xmc.svc.cluster.local
    user: xmc
    essDatabase: ess
    xidDatabase: xid
    sslMode: disable          # in-cluster traffic; use verify-full against a managed service
    passwordSecret:
      name: xmc-postgres-credentials
      key: password
```

The chart renders these into the `db-config` ConfigMap (`DB_BACKEND`, `POSTGRES_*`) and adds a
`POSTGRES_PASSWORD` env var from the Secret to every service that opens the database.

## Upgrades

Re-run the script after pulling a new release: it is idempotent (existing Secret and data are
kept) and the migration Jobs apply only pending migrations.
