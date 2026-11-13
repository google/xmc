# Experiment State Server (ESS)

The **Experiment State Server (ESS)** is the central control plane service in XManager on Cloud (XMC). It manages the lifecycle, execution states, structured status messages, and metadata for distributed experiments, workloads, and work units.

---

## Service Responsibilities

- **Experiment Lifecycle & State Management**: Tracks active, completed, failed, and aborted experiments (`Experiments` and `WorkUnits` tables in Cloud Spanner).
- **gRPC & HTTP APIs**: Implements AIP-compliant APIs defined in `api/protos/experiment/` (`GetExperiment`, `FetchExperimentStatus`, `ListExperiments`, `CreateExperiment`, `UpdateExperiment`).
- **Component Drift Evaluation**: Dynamically evaluates live component drift against locked snapshot generations on read endpoints, propagating warning banners to client consoles (see [Dynamic Drift Detection](#dynamic-drift-detection)).
- **Hyperparameter Optimization Studies**: Validates and persists `STUDY` experiments with their study definition, and pins the components a study requires (see [STUDY Experiments](#study-experiments)).
- **Paused Task Resumption (`resumption/`)**: When `XMC_PAUSE_SCHEDULING_ENABLED` is enabled, runs a background consumer on `xmc-scheduling-resume-stream` (`ess-group`) that claims resumption triggers from the Health Service, fences concurrent workers via `PauseSchedulingState.ResumptionWorkerId`, filters obsolete/terminal tasks, and replays buffered `PausedTasks` back to `xmc-tasks-stream` with backlog throttling.
- **Expired Row Sweeping (`retentionsweep/`)**: Every replica calls `storage.ExpiredRowStore.DeleteExpiredRows` at startup and then every 12 hours to delete expired status messages, status history, processed paused tasks, and CLI login codes. On PostgreSQL an advisory lock lets only one replica sweep at a time; on Cloud Spanner the call is a no-op because `ROW DELETION POLICY` deletes these rows (see [Expired Rows](../../internal/storage/README.md#expired-rows-expiredrowstore)).
- **Authentication & Authorization**: Integrates OAuth2/OIDC token verification and Google Cloud IAM validation (`auth_dev.go` / `auth_prod.go`).

---

## Directory Structure

```
cmd/experiment_state_server/
├── main.go                     # Service entrypoint, flag parsing, and gRPC/HTTP server bootstrap
├── auth_dev.go / auth_prod.go  # Environment-specific authentication interceptors
├── health.go                   # Health check probe handlers (liveness and readiness)
├── resumption/                 # Pause scheduling resumption stream consumer, worker, and task filter
├── retentionsweep/             # Periodic expired-row sweeper (storage.ExpiredRowStore)
├── server/                     # gRPC API handler implementations
│   ├── getexperiment.go        # Fetch single experiment handler
│   ├── getexperimentstatus.go  # Fetch experiment status handler
│   ├── listexperiments.go      # List and filter experiments handler
│   └── ...
└── config/                     # Server configuration definitions
```

---

## Storage Layer

ESS delegates persistence to the storage subsystem defined in [`internal/storage`](../../internal/storage/README.md):
- **Interfaces**: Handlers interact with `storage.Storage` and execute multi-entity atomic mutations via `storage.TransactionRunner` using transaction-scoped repositories (`*storage.Transaction`).
- **Cloud Spanner Backend**: Persistence is implemented in `internal/storage/spanner/` (`experiment/`, `workunit/`, `statusmessage/`, `statushistory/`, `pausestate/`, `pausedtask/`).
- **Hermetic Testing**: Service handlers are unit-tested in memory without database emulators using `storage.MockStorage`.

For details on the transaction model, see [internal/storage/README.md](../../internal/storage/README.md).

---

## Dynamic Drift Detection

When components are updated or deleted in the global catalog, active running experiments may diverge from their locked snapshots. ESS detects this dynamically during read operations without background cron jobs:

1. **Hot-Path Polling Cache**: On high-frequency polling endpoints (`GetExperimentStatus`), ESS caches consistency evaluations for 30 seconds in memory to protect the database from redundant view queries.
2. **Read Path Evaluation**: On `GetExperiment` and `GetExperimentStatus` RPCs, ESS queries the `ExperimentConsistencyInfo` view using the experiment's primary key (`BitReversedXid`).
3. **Warning Propagation**: When divergence is detected, ESS constructs a `StructuredMessage` with `WARNING` severity detailing the specific component generation differences and attaches it to the response `ExperimentStatus`.
4. **Transaction Safety**: Drift checks execute in isolated read-only transactions, ensuring read-write transactions (`FetchExperimentTransaction`) remain strictly side-effect free.

For comprehensive details on the Components subsystem, see [pkg/components/README.md](../../pkg/components/README.md).

---

## Experiment Types

`Experiment.experiment_type` is set by `CreateExperiment` and cannot be changed afterwards; a missing
or `UNSPECIFIED` `experiment_type` is normalized to `STANDARD` before the experiment is stored.
Configuration that only one type uses lives in the `type_config` oneof, and `CreateExperiment`
rejects it with `INVALID_ARGUMENT` when it does not match `experiment_type`.

- `STANDARD`: a regular experiment whose work units are created by the client. It has no
  `type_config`.
- `STUDY`: a hyperparameter optimization study (see [STUDY Experiments](#study-experiments)).

### STUDY Experiments

An experiment with `experiment_type: STUDY` is a hyperparameter optimization study. Its
`study_config` ([`study.proto`](../../api/protos/experiment/study.proto)), the `type_config` member
for studies, carries the search space (`parameters`), the objective (`metrics`), the search
`algorithm`, `max_trial_budget`, and `max_parallel_trials`. Like `experiment_type`, `study_config`
is immutable: it is set only by `CreateExperiment`, returned by `GetExperiment` and
`ListExperiments`, and an `UpdateExperiment` mask that names it fails with `INVALID_ARGUMENT`. It
cannot be used in list filters.

`CreateExperiment` validates the study config before allocating an XID
([`internal/validator/study.go`](../../internal/validator/study.go)) and returns `INVALID_ARGUMENT`
unless:

- `study_config` is set exactly when `experiment_type` is `STUDY`.
- There is exactly one metric (a single objective) with a `MAXIMIZE` or `MINIMIZE` goal and a
  printable `metric_id` of at most 256 bytes without surrounding whitespace.
- `algorithm` is `RANDOM_SEARCH`, `QUASI_RANDOM_SEARCH`, or `GRID_SEARCH`.
- There are 1 to 100 parameters with unique IDs. Trials receive each parameter as a
  `--<parameter_id>=<value>` flag, so IDs must match `^[A-Za-z_][A-Za-z0-9_.-]*$` and be at most 128
  characters long.
- Double and integer bounds satisfy `min_value <= max_value` (equal bounds fix the parameter) and
  double bounds are finite. Categorical and discrete parameters have 1 to 1000 unique values.
  `LOG` and `REVERSE_LOG` scales require positive values, and categorical parameters take no scale.
- `max_trial_budget` and `max_parallel_trials` are positive.

A study requires the `hpo` component, which depends on `vizier` and `clickhouse`. `CreateExperiment`
checks that all three are ready before allocating an XID and again in the transaction that inserts
the experiment, then pins them as `ExperimentComponentSnapshots` at their current generation (see
[Create Experiment](../../docs/flows/experiment_lifecycle.md#1-create-experiment)). Because `hpo` and
`vizier` are `EXPERIMENTAL`, [Dynamic Drift Detection](#dynamic-drift-detection) attaches a
"uses experimental components" `WARNING` to the status returned by `GetExperiment` and
`GetExperimentStatus` while the study is `ACTIVE`; the warning is computed at read time and never stored.

The operator limits study size with the `hpo` component parameter `maxTrialBudget` (default `1000`;
see the [hpo component README](../../deploy/profiles/stable-2026a/components/hpo/README.md)). In the
creation transaction, ESS caps a larger `max_trial_budget` at that limit, stores the effective value,
and appends a warning such as `max_trial_budget 5000 exceeds the hpo component limit; clamped to
1000` to the initial status message, raising it to `WARNING`. Unlike the drift and experimental
components warnings, this warning is persisted with the experiment and in its status message
history. On read, drift detection appends its warnings to the stored message instead of replacing
it, and skips a warning that is already present.

A study's work units are its trials, which only the study controller creates. `CreateWorkUnit` on a
`STUDY` experiment returns `FAILED_PRECONDITION` without writing anything
([`internal/workunit/createworkunit.go`](../../internal/workunit/createworkunit.go)).

---

## Testing & Verification

Run hermetic unit and integration tests using Bazel:

```bash
# Test ESS server handlers
bazel test //cmd/experiment_state_server/...

# Test drift evaluation (caching and warning formatting)
bazel test //internal/experiment:experiment_test

# Test Spanner storage and drift detection queries against the emulator
bazel test //internal/storage/spanner/experiment:experiment_test
```
