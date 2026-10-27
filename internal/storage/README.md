# Storage Package

`internal/storage` defines the persistence interfaces for XMC services and the database backend implementations under [`internal/storage/spanner/`](spanner/) and [`internal/storage/postgres/`](postgres/).

## Architecture

```
XMC Services (ESS, Actuator, Reconciler, etc.)
         │
         │ depends on interfaces
         ▼
internal/storage/
├── storage.go       # Storage, TransactionRunner, and entity store interfaces
└── test_utils.go    # In-memory MockStorage for unit tests
         │
         │ implemented by
         ▼
internal/storage/{spanner,postgres}/
└── Backend repositories, schemas, and migrations
```

## Connection Settings (`dbconfig`)

The Helm chart renders the `database.*` values into the `db-config` ConfigMap, which the XMC workloads load as environment variables. [`dbconfig.Load()`](dbconfig/dbconfig.go) reads `DB_BACKEND` and the selected backend's settings into one typed `Config`, and returns an error for an unknown backend instead of falling back to another backend:

- `DB_BACKEND` selects `spanner` (the default) or `postgres`. The Helm chart accepts `postgres` only with `database.experimentalPostgres: true`.
- `Config.Spanner` carries `SPANNER_ESS_DATABASE`, `SPANNER_XID_DATABASE`, and `SPANNER_QUOTA_PROJECT_ID`.
- `Config.Postgres` carries `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_ESS_DATABASE`, `POSTGRES_XID_DATABASE`, and `POSTGRES_SSLMODE` from `db-config`, plus the optional `POSTGRES_MAX_CONNS` environment override (`0` uses the driver default). Only the port (`5432`) and sslmode (`verify-full`) have defaults, and `EssDSN()` / `XidDSN()` fail when the host, the user or the database is not set. The password never goes into `db-config`: `dbconfig` reads it from `POSTGRES_PASSWORD_FILE` (preferred) or `POSTGRES_PASSWORD`.

## Opening Storage (`factory`)

[`factory.NewStorage(ctx, cfg, db)`](factory/factory.go) opens the storage of one XMC database: `cfg` comes from `dbconfig.Load()`, and `db` is `factory.ESS` or `factory.XID`. The returned `*factory.Storage`:

- serves `storage.Storage` from the backend that `cfg.Backend` selects;
- implements `health.ReadinessChecker` via `CheckReady(ctx)`, which compares the database's schema version with the version that the binary expects for that backend;
- releases its connections on `Close()`, which is safe to call more than once;
- exposes `SpannerClient` on the Spanner backend only, for the components store.

## Schemas and Migrations

Both Cloud Spanner and PostgreSQL maintain canonical full schemas (`schema/{ess,xid}-schema.sql`) and version-aligned SQL migrations (`migrations/{ess,xid}/`):

- **Lockstep schema changes**: Every schema change must update both Spanner ([`spanner/schema/`](spanner/schema/) + [`spanner/migrations/`](spanner/migrations/)) and PostgreSQL ([`postgres/schema/`](postgres/schema/) + [`postgres/migrations/`](postgres/migrations/)) using the same migration version number (`<NNN>_<name>.up.sql` and `<NNN>_<name>.down.sql`).
- **Backend-specific migrations**: If a migration only applies to one backend (such as tuning a backend-specific index), add a same-numbered no-op `.up.sql` / `.down.sql` pair in the other backend so expected schema versions (`EssExpectedSchemaVersion()`, `XidExpectedSchemaVersion()`) remain identical.
- **Automated parity check**: [`migrations/parity_test.go`](migrations/parity_test.go) checks only that expected schema versions and canonical table names match across Spanner and PostgreSQL; it does not compare exact DDL or column definitions, which differ by SQL dialect.

## Transactions (`TransactionRunner`)

Multi-step writes that touch multiple tables (for example, `restartWorkUnitExecutions`) run in the service layer via `TransactionRunner`:

```go
type TransactionRunner interface {
    RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *Transaction) error) error
}
```

Inside `RunInTransaction`, the callback receives a `*storage.Transaction` holding repositories bound to the active read-write transaction:

```go
type Transaction struct {
    WorkUnits      WorkUnitTxRepository
    Experiments    ExperimentTxRepository
    XIDs           XidTxRepository
    StatusMessages StatusMessageTxRepository
    StatusHistory  StatusHistoryTxRepository
    PauseState     PauseStateTxRepository
    PausedTasks    PausedTaskTxRepository
}
```

Example usage:

```go
err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
    wu, err := tx.WorkUnits.FetchWorkUnit(ctx, xid, wid)
    if err != nil {
        return err
    }
    curExec := wu.GetCurrentWorkUnitExecution()
    if err := tx.WorkUnits.SaveWorkUnitExecution(ctx, curExec); err != nil {
        return err
    }
    if err := tx.WorkUnits.UpdateCurrentWorkUnitExecution(ctx, xid, wid, newEID, newExecution); err != nil {
        return err
    }
    exp, err := tx.Experiments.FetchExperiment(ctx, xid)
    if err != nil {
        return err
    }
    return tx.Experiments.UpdateExperimentStatus(ctx, exp)
})
```

> **Note:** Migrating the remaining multi-step transactions (such as `UpdateWorkUnit` and `UpdateExperiment`) out of `internal/storage/spanner/` is tracked in `b/517479143`.

### `Repository` and `TxRepository`

Each Spanner repository subpackage separates standalone operations from transaction-bound operations:

- **`Repository`**: Embedded in `spanner.Storage` to implement the non-transactional `storage.Storage` interface.
- **`TxRepository`**: Returned by `Repository.WithTx(tx)` inside `RunInTransaction` to implement the transactional interfaces on `storage.Transaction`:

```go
type TxRepository struct {
    *Repository
    tx *spanner.ReadWriteTransaction
}
```

PostgreSQL repository subpackages (`internal/storage/postgres/<entity>/`) follow the same split: `NewRepository(pool)` binds the shared `*pgxpool.Pool`, and `Repository.WithTx(tx)` binds the active `pgx.Tx` through the `pgdb.DBTX` interface. `DBTX` and the SQLSTATE error helpers live in the leaf package [`postgres/pgdb/`](postgres/pgdb/), so repository subpackages can import them without an import cycle with `internal/storage/postgres`.

Methods that only buffer mutations into an active read-write transaction (such as `SaveWorkUnitExecution`, `UpdateCurrentWorkUnitExecution`, and `UpdateExperimentStatus`) are declared on `*TxRepository` rather than `*Repository`, so they cannot be called outside `RunInTransaction`.

### Writing `RunInTransaction` Callbacks

Cloud Spanner may abort and re-run the `RunInTransaction` callback when a transaction encounters lock contention (the PostgreSQL backend likewise re-runs it on serialization failures and deadlocks):

1. **Reset state on each attempt**: Initialize or clear any slices, maps, or counters modified by `fn` at the start of the callback, not outside it.
2. **Avoid external side effects**: Do not enqueue tasks or make external RPCs inside `fn`. Perform those after `RunInTransaction` succeeds.
3. **Read through `tx`**: Use `tx.WorkUnits` / `tx.Experiments` for reads inside the callback rather than the non-transactional `Storage`, so reads participate in the read-write transaction.

## Testing

- **Service unit tests**: Use `storage.MockStorage` ([`test_utils.go`](test_utils.go)) to test handler logic in memory. Set `MockStorage.RunInTransactionAttempts = 2` to verify that a transaction callback behaves properly when retried.
- **Spanner integration tests**: Tests under `internal/storage/spanner/...` run against the Spanner emulator to test SQL queries, mutations, and schema constraints.
- **PostgreSQL integration tests**: Tests under `internal/storage/postgres/...` call `postgrestest.Setup(ctx, t)` ([`postgres/testing/`](postgres/testing/)) to get isolated databases with the canonical schema on a shared `postgres:16-alpine` container; their `go_test` targets need `tags = ["local"]` for Docker access. [`postgres/pausestate/`](postgres/pausestate/) holds the reference repository tests, including the concurrency test required by [`AGENTS.md`](AGENTS.md).

```bash
# Unit tests (MockStorage)
bazel test //internal/storage/...

# Spanner emulator tests
bazel test //internal/storage/spanner/...

# PostgreSQL tests (requires Docker)
bazel test //internal/storage/postgres/...
```
