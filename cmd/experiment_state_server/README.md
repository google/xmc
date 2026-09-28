# Experiment State Server (ESS)

The **Experiment State Server (ESS)** is the central control plane service in XManager on Cloud (XMC). It manages the lifecycle, execution states, structured status messages, and metadata for distributed experiments, workloads, and work units.

---

## Service Responsibilities

- **Experiment Lifecycle & State Management**: Tracks active, completed, failed, and aborted experiments (`Experiments` and `WorkUnits` tables in Cloud Spanner).
- **gRPC & HTTP APIs**: Implements AIP-compliant APIs defined in `api/protos/experiment/` (`GetExperiment`, `FetchExperimentStatus`, `ListExperiments`, `CreateExperiment`, `UpdateExperiment`).
- **Component Drift Evaluation**: Dynamically evaluates live component drift against locked snapshot generations on read endpoints, propagating warning banners to client consoles (see [Dynamic Drift Detection](#dynamic-drift-detection)).
- **Authentication & Authorization**: Integrates OAuth2/OIDC token verification and Google Cloud IAM validation (`auth_dev.go` / `auth_prod.go`).

---

## Directory Structure

```
cmd/experiment_state_server/
├── main.go                     # Service entrypoint, flag parsing, and gRPC/HTTP server bootstrap
├── auth_dev.go / auth_prod.go  # Environment-specific authentication interceptors
├── health.go                   # Health check probe handlers (liveness and readiness)
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
- **Cloud Spanner Backend**: Persistence is implemented in `internal/storage/spanner/` (`experiment/`, `workunit/`, `statusmessage/`, `statushistory/`).
- **Hermetic Testing**: Service handlers are unit-tested in memory without database emulators using `storage.MockStorage`.

For details on the transaction model, see [internal/storage/README.md](../../internal/storage/README.md).

---

## Dynamic Drift Detection

When components are updated or deleted in the global catalog, active running experiments may diverge from their locked snapshots. ESS detects this dynamically during read operations without background cron jobs:

1. **Feature Flag Guard**: Drift evaluation is gated by `enable_components` (`cfg.EnableComponents`, default `false`). In environments where components are disabled, drift checks are completely bypassed with zero Spanner queries.
2. **Hot-Path Polling Cache**: On high-frequency polling endpoints (`GetExperimentStatus`), ESS caches consistency evaluations for 30 seconds in memory to protect Cloud Spanner from redundant view queries.
3. **Read Path Evaluation**: On `GetExperiment` and `GetExperimentStatus` RPCs, ESS queries Cloud Spanner's `ExperimentConsistencyInfo` view using the experiment's primary key (`BitReversedXid`).
4. **Warning Propagation**: When divergence is detected, ESS constructs a `StructuredMessage` with `WARNING` severity detailing the specific component generation differences and attaches it to the response `ExperimentStatus`.
5. **Transaction Safety**: Drift checks execute in isolated read-only transactions, ensuring read-write transactions (`FetchExperimentTransaction`) remain strictly side-effect free.

For comprehensive details on the Components subsystem, see [pkg/components/README.md](../../pkg/components/README.md).

---

## Testing & Verification

Run hermetic unit and integration tests using Bazel:

```bash
# Test ESS server handlers
bazel test //cmd/experiment_state_server/...

# Test Spanner storage and drift detection against the emulator
bazel test //internal/storage/spanner/experiment:experiment_test
```
