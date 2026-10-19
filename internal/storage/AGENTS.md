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
- Isolate all database-specific SQL dialects, mutations, schema mappings, and transaction mechanics inside dedicated engine subpackages:
  - [`internal/storage/spanner/`](spanner/) for Cloud Spanner
  - [`internal/storage/postgres/`](postgres/) for PostgreSQL
- Keep Spanner and PostgreSQL schemas and migrations in lockstep (enforced by [`parity_test.go`](migrations/parity_test.go); see [`README.md`](README.md#schemas-and-migrations) for the dual-backend workflow and no-op twin convention).
