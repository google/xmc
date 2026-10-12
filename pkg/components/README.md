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
- **Dynamic Drift Detection (`internal/storage/spanner/experiment/driftdetection.go`)**: Evaluates component drift dynamically on ESS experiment read endpoints (`FetchExperiment`, `FetchExperimentStatus`) by querying Cloud Spanner's `ExperimentConsistencyInfo` view, surfacing warnings to the UI console.

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
- **Structured Spec Field (preferred)**: `ComponentSpec.dependencies` (`repeated string`), populated by the bridge from the Component CR `spec.dependencies` list (e.g. `dependencies: [redpanda]`).
- **Manifests**: `xmc.dev/depends-on` / `xmc.dev/dependencies` annotations or `spec.dependencies` inside raw manifests.
- **Spec Descriptions**: Annotated tags such as `@depends-on: comp-a, comp-b`
- **Labels**: Structured labels like `xmc.dev/dependency.<name>=<version>`

All sources are merged into a single canonical, deduplicated, sorted set that is persisted as the component's dependency edges (Spanner `ComponentDependencies`) and drives cycle detection, deactivation safety, and deletion safety. CMS rejects invalid (non DNS-1123) names and self-references in `ComponentSpec.dependencies` with `InvalidArgument`; the bridge additionally drops self-references (with a warning) before calling CMS.

**Unregistered dependencies & rollout ordering**: both stores require every dependency to already be registered (and not soft-deleted) in CMS, through one shared check in `pkg/components/store/dependency_registration.go`; otherwise `SetComponentIntent` fails with `FailedPrecondition` and a message starting with `store.DependencyNotFoundMessagePrefix` (e.g. `dependency not found: component "ingestion-gateway" depends on "redpanda", which is not registered`). An intent for an existing component is classified against its recorded source first (`planLiveIntent` in `pkg/components/store/intent_change.go`), so a stale intent that also names an unregistered dependency fails with `store.StaleIntentMessagePrefix`, not as a missing dependency. If the bridge reconciles a dependent before its dependency, the dependent reports `DependenciesNotReady` (not an error) and retries its intent every pass until the dependency is registered. Soft-deleting a dependency without its dependents (`DELETION_POLICY_ORPHAN`) drops the edges to it in both stores, and re-registering the same name does not restore them: the dependent's next intent re-links to the new record with a generation bump, and the new record may depend on its former dependents without forming a cycle.

**Idempotent intent**: the bridge re-applies intent on every pass with the CR `metadata.generation` as `ComponentSpec.generation`, so `SetComponentIntent` must be idempotent. Both stores share one definition of an unchanged intent: a desired generation that is not newer than the stored generation (`0` means unspecified), with equal labels, dependencies, and spec (ignoring `spec.generation`). An unchanged intent is a no-op: no generation bump, snapshot, `UpdateTime` write, or re-queue, and the state and status are not reset (CMS state is driven by `UpdateObservedGeneration`), so an `ENABLED` component stays `ENABLED` and its generation does not drift past snapshot locks. Validation still runs first, so dependency cycles and unregistered dependencies are still reported for an unchanged intent. A changed intent sets the generation to the desired generation if newer, else to the stored generation + 1, and resets the state to `COMPONENT_STATE_PENDING_RECONCILIATION`.

**Spanner read-only fast path**: steady-state passes re-apply unchanged intents, so `SpannerStore` first reads the component and its registered dependencies with one strong single-use read. If the intent is unchanged at that read and all of its dependencies are registered and active, it returns the stored record without a read-write transaction; skipping the cycle check is safe because committed state never holds a cycle. A deregistered dependency counts as a change because the read returns only registered dependencies. Any other outcome (a change, a missing or deregistered dependency, a new component, or a read error) falls through to the read-write transaction, which re-reads the catalog and decides authoritatively, so a write that lands between the two reads is never lost. Changed and new intents therefore pay one extra strong read before the read-write transaction.

**Stale intents**: a changed intent whose desired generation is specified but older than the stored generation comes from a stale writer (for example a bridge replica acting on an old CR revision). Both stores reject it through one shared check (`rejectStaleIntent` in `pkg/components/store/intent_change.go`) with `FailedPrecondition` and a message starting with `store.StaleIntentMessagePrefix`, and leave the record unchanged; the bridge reports it as `Ready=False` reason `StaleIntent`. Replaying the stored spec with an older generation stays a no-op, and a changed intent with an equal or unspecified (`0`) generation is applied and bumped as above.

**Legacy inflated generations (unsourced intents)**: records written before intents were idempotent may have a CMS generation above the CR generation. For unsourced intents they keep the higher number: re-applying the unchanged CR is a no-op, but a CR edit is rejected as stale while the CR generation is below the CMS generation. The bridge sends the CR as a sourced intent (below), so its next pass adopts such a record, which keeps its generation and accepts later edits; only other unsourced callers still see the rejection.

**Epoch-ordered intents**: a `SetComponentIntentRequest` with `source` (the uid, creation time and generation of the declarative object, such as the CR) is applied by `SetSourcedComponentIntent`, which orders intents by the source instead of by `spec.generation`, so the CMS generation is independent of the CR generation. Both stores share one planner (`planIntent` in `pkg/components/store/intent_change.go`) and record the adopted source in the `SourceUid`, `SourceGeneration` and `SourceCreateTime` columns:

| Source of the intent vs recorded source | Changed intent | Unchanged intent |
| :--- | :--- | :--- |
| Same uid, generation not older | Applied, generation + 1 | Records a newer source generation, else no-op |
| Same uid, older generation | Rejected as stale | No-op |
| Older object (earlier creation time, or equal time and smaller uid) | Rejected as stale | Rejected as stale |
| Newer object (later creation time, or equal time and greater uid), or none recorded | Adopted, generation + 1 | Adopted: records the source only |

Recording only the source writes no snapshot, generation, state or `UpdateTime`, so a recreated CR (new uid, later creation time) that re-applies the same spec adopts the existing record under the same `ComponentId` and generation, while a lagging replica still acting on the old CR is rejected. Equal creation times (Kubernetes uses one-second granularity) are ordered by uid so every replica decides the same way. A new component starts at generation 1, and an adopted record with a legacy inflated generation keeps it and then advances by 1 per change. Unsourced intents leave the recorded source unchanged. On a record without a source they keep the generation-ordered behavior above; on a sourced record their desired generation comes from the same object as the source, so it is compared with the recorded source generation instead: a changed intent with a specified, older desired generation is rejected as stale, and any other changed intent advances the generation by 1.

**Epoch change observability**: every committed adoption by a newer object (the last table row) logs `Component intent source epoch changed` at Info, with the previous and new uid and creation time, and increments the Prometheus counter `xmc_cms_component_epoch_changes_total`. The counter is registered on the default registry, so every binary that links the store exports it, but only the Experiment State Server, which hosts CMS, increments it; others (for example `cmd/health_service`) export it at 0. It is exposed on the ESS HTTP `/metrics` endpoint; the Helm chart ships no PodMonitoring for ESS, so configure scraping to collect it. The counter is incremented after commit, at most once per adoption: a retried Spanner transaction counts once, and an adoption whose commit outcome is ambiguous (the commit call fails although the write landed) may go unreported. Its only label is `previous_source`: `present` for an adoption from an older object (a recreated CR, or an owner conflict between two objects of one name) and `absent` for the one-time adoption of a record written before sources were recorded. Names and uids are in the log, not in labels, to keep cardinality bounded. Each recreation of a CR adds one `present` increment (an older object cannot adopt the record back), so an increment without a planned recreation means another object of the same name took over the component.

Key functions:
- `ExtractDependencies(spec *componentspb.ComponentSpec, labels map[string]string) []string`: Extracts unique canonical dependency names from all sources above.
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

#### GitOps Bridge Enforcement
The xmc-bridge enforces dependency ordering for Component CRs (see [bridge README](bridge/README.md#state-machine--lifecycle)):
- **Activation**: the bridge always registers an enabled Component's intent (including its dependency edges) with CMS first, then gates actuation. A not-yet-actuated Component whose `spec.dependencies` are missing, disabled, deleting, or not `Ready` at their current generation is held in `COMPONENT_STATE_PENDING_RECONCILIATION` (reason `DependenciesNotReady`). An already-running Component is never torn down during a dependency blip; it reports `DependenciesReady=False` and its Helm upgrades are held (a held upgrade to a new generation reports `Ready=False` reason `HelmUpgradePending`). Dependency cycles report `DependencyCycle`.
- **Deactivation**: setting `spec.enabled: false` calls `PrepareDeactivation` (blocking mode, `force = false`) before tearing down workloads. While active dependents (including gated but registered ones) exist the workload keeps running and the CR reports `DeactivationBlocked` (re-checked at most every 30s); otherwise the bridge deletes the workload and calls `CompleteDeactivation`, reporting `DeactivationIncomplete` and retrying if the handshake fails after teardown. The bridge does not wait for the recommended drain duration.

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

### 6. Dynamic Drift Detection & Warning Propagation (`internal/storage/spanner/experiment/driftdetection.go`)

To alert users when active experiments run against stale or deleted component configurations without introducing periodic background reconciliation loops or polling cron jobs, the Experiment State Server (ESS) evaluates component drift dynamically during experiment read RPCs.

#### Propagation Architecture

```
┌────────────────────────────────────────────────────────┐
│               Cloud Spanner Database                   │
│  ┌────────────────────────┐  ┌──────────────────────┐  │
│  │  Components (Catalog)  │  │ Component Snapshots  │  │
│  └───────────┬────────────┘  └──────────┬───────────┘  │
│              └────────────┬─────────────┘              │
│                           ▼                            │
│         [View: ExperimentConsistencyInfo]              │
│         (ActiveState=1, BitReversedXid PK)             │
└───────────────────────────┬────────────────────────────┘
                            │
              Top-Level Read Entry Points
              (FetchExperiment, FetchExperimentStatus)
                            │
                            ▼
┌────────────────────────────────────────────────────────┐
│            Experiment State Server (ESS)               │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Fast Path: Check IsExperimentConsistent (View)   │  │
│  │ (Zero extra queries when consistent or inactive) │  │
│  ├──────────────────────────────────────────────────┤  │
│  │ Slow Path: Query Drift Details (APPLY_JOIN)      │  │
│  │ (Extract divergent generations & soft-deletions) │  │
│  ├──────────────────────────────────────────────────┤  │
│  │ Attach Warning to StructuredStatusMessage        │  │
│  │ (Upgrade < WARNING to WARNING; preserve ERROR)   │  │
│  └──────────────────────────────────────────────────┘  │
└───────────────────────────┬────────────────────────────┘
                            │
               ESS Read Responses (gRPC / REST)
                            │
                            ▼
┌────────────────────────────────────────────────────────┐
│                   XMC UI Console                       │
│    Renders Component Drift Banner & Warning Notice     │
└────────────────────────────────────────────────────────┘
```

#### Key Design Invariants

1. **Direct View Querying Without CMS Overhead**:
   - ESS queries the Cloud Spanner view `ExperimentConsistencyInfo` directly using the experiment's primary key (`BitReversedXid = @bitReversedXid`), bypassing CMS gRPC hops and avoiding full-table scans.
2. **Terminal Workload Filtering**:
   - The view filters exclusively by `WHERE e.ActiveState = 1`. Inactive and terminal experiments automatically return no rows from the view, bypassing drift evaluation entirely and preserving terminal snapshot records.
3. **Optimized Two-Tier Query Strategy**:
   - **Fast Path**: Evaluates `IsExperimentConsistent BOOL` and `HasPendingReconciliation BOOL` from the view. If consistent (`true`) or no snapshots exist, no additional queries are executed.
   - **Slow Path**: Only when `IsExperimentConsistent == false`, ESS issues a secondary query joining `ExperimentComponentSnapshots` and `Components` using the `JOIN@{JOIN_METHOD=APPLY_JOIN}` optimizer hint to retrieve specific component names, locked snapshot generations, and live observed generations (bounded to `maxDriftDetails = 10`).
4. **Resilient Status Message Merging**:
   - Preserves higher-severity messages (`ERROR`, `FATAL`) untouched.
   - Upgrades lower-severity messages (`INFO`, `UNSPECIFIED`) to `WARNING` severity and appends human-readable drift details without losing existing status text.
   - Eliminates duplicate drift notices on repeated read calls.
5. **Strict Transaction Isolation**:
   - Drift evaluation executes exclusively within top-level read entry points (`FetchExperiment`, `FetchExperimentStatus`) using independent read-only transactions (`ReadOnlyTransaction`).
   - Core read-write transaction methods (`FetchExperimentTransaction`, `fetchExperimentStatusTransaction`) remain completely side-effect free, preventing phantom reads, transaction aborts, or concurrent mutation deadlocks.
6. **Feature Flag Gating (`enable_components`)**:
   - Drift detection is strictly gated by the server configuration flag `enable_components` (`cfg.EnableComponents`). When `enable_components = false` (default across non-component clusters), drift evaluation is bypassed immediately, guaranteeing zero extra Spanner queries and zero regression risk on unmigrated databases.
7. **Hot-Path Polling Cache (30-second TTL)**:
   - To mitigate high-frequency polling overhead on `GetExperimentStatus` (where the UI console or CLI commands poll status every 1–2 seconds), ESS maintains a thread-safe in-memory cache with a 30-second TTL. If status is polled multiple times within 30 seconds, consistency evaluation is served directly from memory, eliminating redundant Spanner view queries.

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

---

## 6. Workload Consumption & Runtime Injection (`cmd/actuator`)

WorkUnits consume components at runtime through the Actuator daemon without querying Cloud Spanner directly during JobSet synthesis:

1. **Snapshot Pinning**: During WorkUnit creation, the Experiment State Server locks component generations into the `xmc.dev/component-snapshots` annotation on the JobSet executable.
2. **Controller-Runtime Lookup**: Actuator reads `Component` custom resources (`xmc.dev/v1alpha1`) via `controller-runtime` (target namespace with cluster-wide `"xmc"` fallback) without querying Cloud Spanner.
3. **Placement Gating**: Actuator blocks placement and requeues the task if required components are missing (hydration lag), disabled, or unhealthy (`Ready=False`/`Unknown` or `Degraded=True`).
4. **Drift Warning**: If a component's generation drifts past the snapshot lock, Actuator allows placement but logs a warning and annotates the JobSet with `xmc.dev/component-drift-warning`.
5. **Configuration Injection**: Actuator injects plain environment variables (`status.access.envVars`) into all primary and init containers across all replicated jobs.
