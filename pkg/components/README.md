# XMC Components Subsystem

The **XMC Components Subsystem** provides lifecycle management, dependency resolution, versioning, and validation for reusable AI software modules and configurations in XManager on Cloud (XMC).

---

## Architecture Overview

The Components subsystem coordinates component definitions, versioning, and runtime lifecycle across database storage, control planes, and cluster nodes:

- **Source of Truth (SOT)**: Google Cloud Spanner acts as the authoritative relational database storing the global component catalog, versions, dependencies, and rollout states.
- **Component Manager Service (CMS)**: A gRPC service hosted within the Experiment Service Server (ESS) providing control plane APIs for component registration, dependency validation, and lifecycle operations.
- **GitOps Bridge (`cmd/xmc-bridge`)**: A bidirectional synchronizer reconciling Kubernetes Custom Resources (`Component.xmc.dev`) with the authoritative Spanner catalog.
- **Production Spanner Store (`pkg/components/store/spanner_store.go`)**: A production Cloud Spanner persistence driver with ACID read-write transactions, prospective cycle checking, soft-deletion active name index release, and snapshot queries.
- **In-Memory Store (`pkg/components/store/memory_store.go`)**: A thread-safe in-memory cache and test double implementation.

```
┌─────────────────────────────────────────────────────────┐
│              Experiment Service Server (ESS)            │
│  ┌───────────────────────────────────────────────────┐  │
│  │         Component Manager Service (CMS)           │  │
│  └─────────────────────────┬─────────────────────────┘  │
│                            │                            │
│  ┌─────────────────────────▼─────────────────────────┐  │
│  │         Storage Engine (SpannerStore / Memory)    │  │
│  │   ┌─────────────────────┬─────────────────────┐   │  │
│  │   │ Dependency Parser   │ Graph Validator     │   │  │
│  │   │ & Normalization     │ (3-Color DFS)       │   │  │
│  │   ├─────────────────────┼─────────────────────┤   │  │
│  │   │ Deactivation Engine │ Snapshot Resolver   │   │  │
│  │   └─────────────────────┴─────────────────────┘   │  │
│  └───────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

---

## Subsystem Modules

### 1. Dependency Extraction & Normalization (`pkg/components/store/dependency_parser.go`)

Components specify explicit dependencies on other components via:
- **Spec Descriptions**: Annotated tags such as `@depends-on: comp-a, comp-b`
- **Labels**: Structured labels like `xmc.dev/dependency.<name>=<version>`

Key functions:
- `ExtractDependencies(spec *componentspb.ComponentSpec, labels map[string]string) []string`: Extracts unique canonical dependency names.
- `NormalizeDependencies(deps []string) []string`: Strips `components/` prefixes, deduplicates, and sorts lexicographically.

---

### 2. Dependency Cycle Detection (`pkg/components/store/graph_validator.go`)

To prevent circular deadlocks during runtime activation and initialization, XMC validates the component dependency DAG on every write operation (`SetComponentIntent`) before mutating store state.

#### 3-Color DFS Cycle Detection Algorithm
The validator utilizes a canonical 3-color depth-first search (DFS):
- **White (`0`)**: Unvisited node.
- **Gray (`1`)**: Currently exploring in active recursion stack (ancestor).
- **Black (`2`)**: Fully explored node and all reachable descendants.

When a traversal encounters a **Gray** node, a back-edge indicating a dependency cycle has been detected. The DFS reconstructs the exact cycle path (e.g., `comp-a -> comp-b -> comp-c -> comp-a`).

```
  [comp-a (Gray)] ──► [comp-b (Gray)] ──► [comp-c (Gray)]
        ▲                                       │
        └───────────────────────────────────────┘ (Back-edge detected!)
```

#### Prospective Cycle Validation
- `ValidateProspectiveDependencies(existing []*ComponentRecord, targetName string, newDeps []string) error`
- `ValidateProspectiveDependenciesFromMap(components map[string]*ComponentRecord, targetName string, newDeps []string) error`

If a prospective update would introduce a self-cycle or transitive cycle, the operation is rejected immediately with gRPC `codes.FailedPrecondition` and `ErrDependencyCycle`.

---

### 3. Cascading Deactivation & Deletion Engine (`pkg/components/store/deactivation_engine.go`)

Components cannot be deactivated or deleted arbitrarily if other active components depend on them. The deactivation engine enforces lifecycle safety boundaries.

#### Deactivation Safety & Cascading
- `EvaluateDeactivationSafety(components map[string]*ComponentRecord, targetName string, requestCascade bool, defaultDrain time.Duration) (*PrepareDeactivationResult, error)`
  - **Blocking Mode (`requestCascade = false`)**: If non-deleted, non-disabled dependents exist, deactivation is blocked with `ErrHasActiveDependents` (`codes.FailedPrecondition`) and returns the list of blocking dependents.
  - **Cascading Mode (`requestCascade = true`)**: Identifies all active transitive dependents in reverse topological order, transitioning both the target and its dependents to `COMPONENT_STATE_DISABLING`.

#### Draining Timeout Calculation
- `CalculateDrainTimeout(rec *ComponentRecord, cascaded []*ComponentRecord, reqTimeout *durationpb.Duration) time.Duration`
  - Considers explicit request overrides (`reqTimeout`), individual component spec configurations (`LifecycleControl.DrainingTimeout` / `GracePeriod`), and enforces a default minimum of 30 seconds (`minDrainTimeout = 30 * time.Second`).

#### Deletion Protection Safety
- `IsDeletionProtected(rec *ComponentRecord) bool`: Checks for `xmc.dev/deletion-protection` or `deletion_protection` labels set to `"true"` or `"1"`.
- `ValidateDeletionSafety(components map[string]*ComponentRecord, targetName string, policy componentspb.DeletionPolicy, force bool) error`:
  - Blocks deletion if the component is deletion-protected or has active dependents unless `force = true`.
  - With `DELETION_POLICY_DELETE` or `DELETION_POLICY_UNSPECIFIED`, automatically cascades deletion across all dependents.

---

### 4. Component Manager Service (CMS) Lifecycle Handlers (`pkg/components/cms/server.go`)

The `ComponentManagerService` exposes standard AIP-compliant gRPC RPCs for external control planes (e.g., GitOps bridge, CLI, ESS UI).

#### RPC Endpoints
- `PrepareDeactivation(ctx, req *PrepareDeactivationRequest) (*PrepareDeactivationResponse, error)`:
  - Validates named request resource paths (`components/{name}`).
  - Validates `draining_timeout` via `dt.CheckValid()`.
  - Computes drain duration via `calculateEffectiveDrainSeconds` with sub-second ceiling rounding and `math.MaxInt32` overflow protection.
  - Initiates draining and transitions target and cascaded dependents to `COMPONENT_STATE_DISABLING`.
- `CompleteDeactivation(ctx, req *CompleteDeactivationRequest) (*CompleteDeactivationResponse, error)`:
  - Finalizes deactivation, transitioning component from `COMPONENT_STATE_DISABLING` to `COMPONENT_STATE_DISABLED`.
  - Idempotent: returns success (`nil`) if component is already in `DISABLED` state.
  - Supports `force = true` to override remaining active dependent checks.
- `ExecuteDeletion(ctx, req *ExecuteDeletionRequest) (*ExecuteDeletionResponse, error)`:
  - Enforces deletion protection labels and active dependent safety.
  - Transitions component to `COMPONENT_STATE_DELETED` and records deletion timestamp.

---

### 5. Production Cloud Spanner Persistence Driver (`pkg/components/store/spanner_store.go`)

`SpannerStore` provides the production database persistence layer implementing the `store.Store` interface against Cloud Spanner.

#### Relational Table Schema
- **`Components`**: Stores component intent and status (`ComponentId`, `Name`, `ComponentClass`, `ConfigProto`, `AccessPayload`, `State`, `Generation`, `ObservedGeneration`, `CreateTime`, `UpdateTime`, `ActiveName`).
- **`ComponentDependencies`**: Relational dependency graph (`ComponentId`, `DependencyComponentId`) interleaved in `Components` with `ON DELETE CASCADE` and foreign key constraints.
- **`ExperimentComponentSnapshots`**: Immutable historical generation snapshots locked to experiment runs (`ComponentId`, `LockedGeneration`, `ConfigProto`, `AccessPayload`).

#### Key Architectural Invariants
1. **Strict Query Parameterization**: All queries use static statements with `@parameter` binding (`spanner.Statement`), eliminating SQL injection risks.
2. **Soft-Deletion & ActiveName Unique Index Release**:
   - Schema defines: `ActiveName STRING(MAX) AS (IF(State != 5, Name, NULL)) STORED` with `CREATE UNIQUE NULL_FILTERED INDEX Idx_Components_ActiveName ON Components(ActiveName)`.
   - When a component is soft-deleted (`State = 5`), Spanner evaluates `ActiveName` to `NULL`, immediately releasing the unique index constraint to allow reusing the component name with a new `ComponentId` while preserving historical snapshot references and foreign keys.
3. **Lock Footprint Optimization**:
   - Frequent controller synchronization loops (`UpdateObservedGeneration`) and deactivation status checks use targeted parameterized queries (`queryComponentWithDeps`, `queryActiveDependents`), preventing table-wide shared locks and transaction contention.
4. **Historical Generation Snapshots**:
   - Resolves live generation coordinates from `Components` or historical pinned coordinates from `ExperimentComponentSnapshots`.

---

## Component Lifecycle State Machine

```
              ┌────────────────────────┐
              │ COMPONENT_STATE_ACTIVE │
              └───────────┬────────────┘
                          │ PrepareDeactivation (safe or force=true)
                          ▼
            ┌───────────────────────────┐
            │ COMPONENT_STATE_DISABLING │
            └─────────────┬─────────────┘
                          │ CompleteDeactivation (draining finished)
                          ▼
            ┌───────────────────────────┐
            │ COMPONENT_STATE_DISABLED  │
            └─────────────┬─────────────┘
                          │ ExecuteDeletion (safe or force=true)
                          ▼
            ┌───────────────────────────┐
            │  COMPONENT_STATE_DELETED  │
            └───────────────────────────┘
```

### State Machine Guarantees
1. **Monotonic Progression**: States move strictly forward (`ACTIVE` ➔ `DISABLING` ➔ `DISABLED` ➔ `DELETED`).
2. **Idempotency**: Deactivation operations are idempotent and will not error if called repeatedly on terminal/stable states.
3. **Transition Timestamps**: Every state transition updates `rec.Status.LastTransitionTime` and regenerates the record `Etag`.


