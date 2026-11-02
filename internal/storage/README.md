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
- implements `health.ReadinessChecker` via `CheckReady(ctx)`, which compares the database's schema version with the version that the binary expects for that backend, and exposes both via `SchemaVersion()`;
- releases its connections on `Close()`, which is safe to call more than once.

## Schemas and Migrations

Both Cloud Spanner and PostgreSQL maintain canonical full schemas (`schema/{ess,xid}-schema.sql`) and version-aligned SQL migrations (`migrations/{ess,xid}/`):

- **Lockstep schema changes**: Every schema change must update both Spanner ([`spanner/schema/`](spanner/schema/) + [`spanner/migrations/`](spanner/migrations/)) and PostgreSQL ([`postgres/schema/`](postgres/schema/) + [`postgres/migrations/`](postgres/migrations/)) using the same migration version number (`<NNN>_<name>.up.sql` and `<NNN>_<name>.down.sql`).
- **Backend-specific migrations**: If a migration only applies to one backend (such as tuning a backend-specific index), add a same-numbered no-op `.up.sql` / `.down.sql` pair in the other backend so expected schema versions (`EssExpectedSchemaVersion()`, `XidExpectedSchemaVersion()`) remain identical.
- **Automated parity check**: [`migrations/parity_test.go`](migrations/parity_test.go) checks only that expected schema versions and canonical table names match across Spanner and PostgreSQL; it does not compare exact DDL or column definitions, which differ by SQL dialect.

## Expired Rows (`ExpiredRowStore`)

Some ESS tables keep rows only for a limited time:

| Table | Rows expire |
| :--- | :--- |
| `StatusMessages` | 30 days after `CreateTime` (180 days for `ERROR` and `FATAL`) |
| `WorkUnitStatusHistory` | 30 days after `CreateTime` |
| `PausedTasks` | 7 days after `ProcessedTime` (pending tasks never expire) |
| `CliLoginCodes` | 1 day after `ExpiresAt` |

Cloud Spanner deletes these rows itself through the `ROW DELETION POLICY` of each table, so the Spanner `DeleteExpiredRows` is a no-op. PostgreSQL has no row TTL, so [`postgres/retention/`](postgres/retention/) deletes them: each `DeleteExpiredRows(ctx, batchSize)` call runs one transaction that takes `pg_try_advisory_xact_lock` and, only if it gets the lock, deletes up to `batchSize` expired rows from each table. A call that does not get the lock deletes nothing and returns 0, so concurrent callers never sweep at the same time. ESS calls it periodically from [`retentionsweep`](../../cmd/experiment_state_server/retentionsweep/) on every replica (see that package for the schedule and batch limits).

When you add a retention policy to a table, update both the Spanner `ROW DELETION POLICY` and the PostgreSQL delete statements in [`postgres/retention/`](postgres/retention/).

## Transactions (`TransactionRunner`)

Multi-step writes that touch multiple tables (for example, `restartWorkUnitExecutions`) run in the service layer via `TransactionRunner`:

```go
type TransactionRunner interface {
    RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *Transaction) error) error
    RunInSerializableTransaction(ctx context.Context, fn func(ctx context.Context, tx *Transaction) error) (time.Time, error)
}
```

`RunInSerializableTransaction` runs `fn` in a serializable read-write transaction with the same callback and retry rules as `RunInTransaction`, and returns the backend's commit timestamp (the value written to commit-time columns such as `UpdateTime`) so callers can populate timestamps without reading rows back. For reads that must come from one consistent snapshot without a write lock, `ComponentRepository.WithReadOnlySnapshot` passes `fn` a read-only `ComponentTxRepository`; writes through it fail with `codes.FailedPrecondition`.

Inside `RunInTransaction` (and `RunInSerializableTransaction`), the callback receives a `*storage.Transaction` holding repositories bound to the active read-write transaction:

```go
type Transaction struct {
    WorkUnits      WorkUnitTxRepository
    Experiments    ExperimentTxRepository
    XIDs           XidTxRepository
    StatusMessages StatusMessageTxRepository
    StatusHistory  StatusHistoryTxRepository
    PauseState     PauseStateTxRepository
    PausedTasks    PausedTaskTxRepository
    Components     ComponentTxRepository
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

PostgreSQL repository subpackages (`internal/storage/postgres/<entity>/`) follow the same split: `NewRepository(pool)` binds the shared `*pgxpool.Pool`, and `Repository.WithTx(tx)` (on repositories that participate in `storage.Transaction`) binds the active `pgx.Tx` through the `pgdb.DBTX` interface, while repositories that open their own read-write or multi-statement read-only transactions hold `*pgxpool.Pool` and invoke `pgdb.RunInTx` or `pgdb.RunInReadOnlyTx` directly. `DBTX`, `RunInTx`, `RunInReadOnlyTx`, `List`, and the SQLSTATE error helpers live in the leaf package [`postgres/pgdb/`](postgres/pgdb/), and row structs / protobuf converters live in [`postgres/data/`](postgres/data/) (mirroring [`spanner/data/`](spanner/data/)), so repository subpackages can import them without an import cycle with `internal/storage/postgres`.

Methods that only buffer mutations into an active read-write transaction (such as `SaveWorkUnitExecution`, `UpdateCurrentWorkUnitExecution`, and `UpdateExperimentStatus`) are declared on `*TxRepository` rather than `*Repository`, so they cannot be called outside `RunInTransaction`.

### Writing `RunInTransaction` Callbacks

Cloud Spanner may abort and re-run the `RunInTransaction` or `RunInSerializableTransaction` callback when a transaction encounters lock contention (the PostgreSQL backend likewise re-runs it on serialization failures and deadlocks):

1. **Reset state on each attempt**: Initialize or clear any slices, maps, or counters modified by `fn` at the start of the callback, not outside it.
2. **Avoid external side effects**: Do not enqueue tasks or make external RPCs inside `fn`. Perform those after `RunInTransaction` / `RunInSerializableTransaction` succeeds.
3. **Read through `tx`**: Use `tx.WorkUnits` / `tx.Experiments` for reads inside the callback rather than the non-transactional `Storage`, so reads participate in the read-write transaction.

### Isolation

Spanner read-write transactions are always serializable. PostgreSQL runs `RunInTransaction` at `READ COMMITTED` by default, and repositories lock the rows they read and write back (see rule 5 of [`AGENTS.md`](AGENTS.md)). Callers whose invariants span many rows, such as the component store in [`pkg/components/store/dbstore/`](../../pkg/components/store/dbstore/), call `RunInSerializableTransaction` (backed by `pgdb.RunInSerializableTx` at `SERIALIZABLE` isolation) instead; PostgreSQL then aborts conflicting transactions with `40001`, and the callback is retried.

## Testing

- **Service unit tests**: Use `storage.MockStorage` ([`test_utils.go`](test_utils.go)) to test handler logic in memory. Set `MockStorage.RunInTransactionAttempts = 2` to verify that a transaction callback behaves properly when retried. Set `MockStorage.CommitTime` to pin the timestamp returned by `RunInSerializableTransaction`; when it is zero, the mock returns `time.Now()`.
- **Spanner integration tests**: Tests under `internal/storage/spanner/...` run against the Spanner emulator to test SQL queries, mutations, and schema constraints.
- **PostgreSQL integration tests**: Tests under `internal/storage/postgres/...` call `postgrestest.Setup(ctx, t)` ([`postgres/testing/`](postgres/testing/)) to get isolated databases with the canonical schema on a shared `postgres:16-alpine` container; their `go_test` targets need `tags = ["local"]` for Docker access. [`postgres/operation/`](postgres/operation/) and [`postgres/pausestate/`](postgres/pausestate/) hold the reference repository tests, including the concurrency test required by [`AGENTS.md`](AGENTS.md).
- **Component store tests**: [`pkg/components/store/dbstore/`](../../pkg/components/store/dbstore/) runs its cross-backend tests on both Spanner and PostgreSQL (one `dbBackends()` entry each), and its PostgreSQL concurrency tests check that `SERIALIZABLE` transactions that would break catalog invariants are aborted and retried.

```bash
# Unit tests (MockStorage)
bazel test //internal/storage/...

# Spanner emulator tests
bazel test //internal/storage/spanner/...

# PostgreSQL tests (requires Docker)
bazel test //internal/storage/postgres/...
```
