# Storage Package

`internal/storage` defines the persistence interfaces for XMC services and the Cloud Spanner implementation under `internal/storage/spanner/`.

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
internal/storage/spanner/
└── Cloud Spanner repositories and RunInTransaction implementation
```

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

Methods that only buffer mutations into an active read-write transaction (such as `SaveWorkUnitExecution`, `UpdateCurrentWorkUnitExecution`, and `UpdateExperimentStatus`) are declared on `*TxRepository` rather than `*Repository`, so they cannot be called outside `RunInTransaction`.

### Writing `RunInTransaction` Callbacks

Cloud Spanner may abort and re-run the `RunInTransaction` callback when a transaction encounters lock contention:

1. **Reset state on each attempt**: Initialize or clear any slices, maps, or counters modified by `fn` at the start of the callback, not outside it.
2. **Avoid external side effects**: Do not enqueue tasks or make external RPCs inside `fn`. Perform those after `RunInTransaction` succeeds.
3. **Read through `tx`**: Use `tx.WorkUnits` / `tx.Experiments` for reads inside the callback rather than the non-transactional `Storage`, so reads participate in the read-write transaction.

## Testing

- **Service unit tests**: Use `storage.MockStorage` ([`test_utils.go`](test_utils.go)) to test handler logic in memory. Set `MockStorage.RunInTransactionAttempts = 2` to verify that a transaction callback behaves properly when retried.
- **Spanner integration tests**: Tests under `internal/storage/spanner/...` run against the Spanner emulator to test SQL queries, mutations, and schema constraints.

```bash
# Unit tests (MockStorage)
bazel test //internal/storage/...

# Spanner emulator tests
bazel test //internal/storage/spanner/...
```
