# Storage Layer Guidelines (`internal/storage`)

Follow these rules when adding or modifying code under `internal/storage/`:

## 1. No Business Logic in Storage
- Storage packages are strictly responsible for data persistence (queries, mutations, row scanning, proto marshaling) and transaction execution.
- Do **not** place business logic, state transition rules, status diffing, child state count calculations, or multi-step domain orchestration in `internal/storage` or its backend subpackages.
- Business orchestration belongs in the service layer (for example, `cmd/experiment_state_server/` or `internal/`).

## 2. Manage Transaction Boundaries in the Caller (`TransactionRunner`)
- Multi-step or cross-entity read-modify-write workflows must be orchestrated outside the storage package using `storage.TransactionRunner.RunInTransaction`:
  ```go
  err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
      // Orchestrate reads and writes via tx.WorkUnits, tx.Experiments, etc.
  })
  ```
- Expose composable, single-responsibility read and write primitives on the transaction-scoped repositories (`WorkUnitTxRepository`, `ExperimentTxRepository`, `StatusMessageTxRepository`, `StatusHistoryTxRepository`, `PauseStateTxRepository`, `PausedTaskTxRepository`) instead of creating monolithic transaction methods inside storage backends (see [`README.md`](README.md)).
- Inside `RunInTransaction` callbacks, propagate storage errors with `%w` so backend transaction runners can detect and retry transient contention errors (such as `status.Aborted`).

## 3. Keep Database-Specific Logic in Dedicated Subpackages
- The root `internal/storage` package ([`storage.go`](storage.go), [`test_utils.go`](test_utils.go)) must remain 100% database-agnostic. Never import database drivers or engine-specific types into `internal/storage`.
- Service and command layers (`cmd/`, `internal/`) must depend on `internal/storage` (`storage.Storage` or its domain sub-interfaces) rather than importing `internal/storage/spanner/<domain>` or `internal/storage/postgres/<domain>` directly.
- Services load database connection settings through [`internal/storage/dbconfig`](dbconfig/) (fed by the shared `db-config` ConfigMap) instead of reading backend-specific environment variables themselves (see [`README.md`](README.md#connection-settings-dbconfig)).
- Services open their storage with [`internal/storage/factory`](factory/) (`factory.NewStorage`) instead of creating Spanner clients or PostgreSQL pools, and check database readiness via `health.ReadinessChecker` with the returned `*factory.Storage`'s `CheckReady` (see [`README.md`](README.md#opening-storage-factory)).
- Every new domain repository must be wired into `storage.Storage` ([`storage.go`](storage.go)), [`internal/storage/spanner/spanner.go`](spanner/spanner.go), [`internal/storage/postgres/storage.go`](postgres/storage.go) (returning `codes.Unimplemented` in PostgreSQL if not yet implemented), and `storage.MockStorage` ([`test_utils.go`](test_utils.go)).
- Isolate all database-specific SQL dialects, mutations, schema mappings, and transaction mechanics inside dedicated engine subpackages:
  - [`internal/storage/spanner/`](spanner/) for Cloud Spanner
  - [`internal/storage/postgres/`](postgres/) for PostgreSQL
- Keep Spanner and PostgreSQL schemas and migrations in lockstep (enforced by [`parity_test.go`](migrations/parity_test.go); see [`README.md`](README.md#schemas-and-migrations) for the dual-backend workflow and no-op twin convention).

## 4. Deterministic Default Ordering & Pagination for Unbounded Collections
- **Explicit Newest-First Default Ordering (No Default Primary-Key ASC for Feeds)**: `TableView` and query generators for user-created, time-series, or unbounded entities MUST define an explicit default ordering via `OrderByFieldsWithSortDirection` (typically `created DESC` with a unique primary-key tie-breaker such as `id ASC`; hierarchical execution children scoped to a single parent such as `work_units` may order by sequence ID `ASC`). Never omit default ordering or fall back to primary-key or lexicographical `ASC` ordering on unbounded feeds, which truncates newly created entities from the first page once shared environments accumulate >100 historical records.
- **Server-Side Filtering & Pagination**: Always implement server-side filtering (`AIP-160`) and page-token pagination on `List*` queries instead of expecting callers to consume single-page results.
- **Multi-Page Pagination Test Coverage**: Unit and integration test suites for `List*` repositories MUST test pagination traversal across multiple pages (`N > page_size`) and verify that default sorting (empty `orderBy`) returns the view's declared default order (newest-first for feeds) consistently across page boundaries.

## 5. PostgreSQL Row Locking in Transactions
`postgres.RunInTx` runs `RunInTransaction` callbacks at PostgreSQL's default `READ COMMITTED` isolation and retries `40001`/`40P01`. A plain `SELECT` takes no row lock at this level, so concurrent read-modify-write transactions silently overwrite each other unless the read locks its rows. [`postgres/pausestate/`](postgres/pausestate/) is the reference implementation.
- **Lock read-modify-write reads with `FOR NO KEY UPDATE`**: Any `*TxRepository` read whose result the same transaction writes back (such as `FetchExperiment`, `FetchWorkUnit`, `FetchUpdatingWorkUnits`, `FetchXidEntry`, and `PauseStateTxRepository.Get`) must end with `FOR NO KEY UPDATE`. Never use `FOR UPDATE`: it conflicts with the `FOR KEY SHARE` lock that foreign-key checks take on a parent row when child rows are inserted, which deadlocks concurrent writers.
- **Lock batches in primary-key order**: Multi-row locking reads must `ORDER BY` the primary key so concurrent transactions acquire row locks in the same order.
- **Lock the parent before allocating child IDs**: Aggregates such as `MAX(...)` cannot be locked. Run `SELECT 1 FROM <Parent> WHERE <Key> = $1 FOR NO KEY UPDATE` as a separate statement before computing the next child ID (such as `FetchNextWorkUnitID`) and before inserting a child row with a caller-supplied ID into the same parent (such as `InsertWorkUnit`).
- **Seed singleton rows before locking them**: Locking a missing row locks nothing. For singleton tables, `INSERT ... ON CONFLICT DO NOTHING` the default row first, then lock it with `SELECT ... FOR NO KEY UPDATE`.
- **Keep multi-statement reads consistent**: Non-transactional reads that span several statements must use a single SQL statement or a `REPEATABLE READ READ ONLY` transaction.
- **Prove each lock with a concurrency test**: Every read-modify-write path needs a [`postgrestest`](postgres/testing/)-backed test that runs concurrent `RunInTransaction` callbacks against the same row and asserts that no update is lost.
