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
- **Dynamic Drift Detection (`internal/experiment/driftdetection.go`)**: Evaluates component drift dynamically on ESS experiment read endpoints (`GetExperiment`, `GetExperimentStatus`) by querying Cloud Spanner's `ExperimentConsistencyInfo` view, surfacing warnings to the UI console.

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

**Unregistered dependencies & rollout ordering**: both stores require every dependency to already be registered (and not soft-deleted) in CMS, through one shared check in `pkg/components/store/dependency_registration.go`; otherwise `SetComponentIntent` fails with `FailedPrecondition`, a `google.rpc.ErrorInfo` with domain `components.xmc.dev` (`store.ErrorInfoDomain`), reason `DEPENDENCY_NOT_REGISTERED` and metadata `operation`/`component`/`dependency`, and a message starting with `store.DependencyNotFoundMessagePrefix` (e.g. `dependency not found: component "ingestion-gateway" depends on "redpanda", which is not registered`). If the bridge reconciles a dependent before its dependency, the dependent reports `DependenciesNotReady` (not an error) and retries its intent every pass until the dependency is registered. Soft-deleting a dependency without its dependents (`DELETION_POLICY_ORPHAN`) drops the edges to it in both stores, and re-registering the same name does not restore them: the dependent's next intent re-links to the new record with a generation bump, and the new record may depend on its former dependents without forming a cycle.

**Idempotent intent**: the bridge re-applies intent on every pass with the CR `metadata.generation` as `ComponentSpec.generation`, so `SetComponentIntent` must be idempotent. Both stores share one definition of an unchanged intent: a desired generation that is not newer than the stored generation (`0` means unspecified), with equal labels, dependencies, and spec (ignoring `spec.generation`). An unchanged intent is a no-op: no generation bump, snapshot, `UpdateTime` write, or re-queue, and the state and status are not reset (CMS state is driven by `UpdateObservedGeneration`), so an `ENABLED` component stays `ENABLED` and its generation does not drift past snapshot locks. Validation still runs first, so dependency cycles and unregistered dependencies are still reported for an unchanged intent. A changed intent sets the generation to the desired generation if newer, else to the stored generation + 1, and resets the state to `COMPONENT_STATE_PENDING_RECONCILIATION`.

**Spanner read-only fast path**: steady-state passes re-apply unchanged intents, so `SpannerStore` first reads the component and its registered dependencies with one strong single-use read. If the intent is unchanged at that read and all of its dependencies are registered and active, it returns the stored record without a read-write transaction; skipping the cycle check is safe because committed state never holds a cycle. A deregistered dependency counts as a change because the read returns only registered dependencies. Any other outcome (a change, a missing or deregistered dependency, a new component, or a read error) falls through to the read-write transaction, which re-reads the catalog and decides authoritatively, so a write that lands between the two reads is never lost. Changed and new intents therefore pay one extra strong read before the read-write transaction.

**Legacy inflated generations**: records written before intents were idempotent may have a CMS generation above the CR generation. They keep the higher number, and later CR edits bump from it. This affects only pre-fix development data because Components have not launched; recreate affected records to realign CMS and CR generations.

**Error contract**: machine-readable store rejections carry a `google.rpc.ErrorInfo` with domain `components.xmc.dev` (`store.ErrorInfoDomain`, built by `errorWithInfo` in `pkg/components/store/error_info.go`); callers branch on its `reason`, never on the message. They all use code `FailedPrecondition`, never `Aborted`, which the Spanner client retries when a read-write transaction body returns it. Every reason carries `operation`, the RPC name, in its metadata: `DEPENDENCY_NOT_REGISTERED` and `DEPENDENCY_CYCLE` (`SetComponentIntent`), and `ACTIVE_DEPENDENTS_EXIST` (`PrepareDeactivation`, non-cascading `ExecuteDeletion`). The bridge still matches the legacy `DependencyNotFoundMessagePrefix` and the cycle message until it switches to the reasons.

**Sourced intents & owner**: a `SetComponentIntentRequest` with `source` (a `RequestSource`: the CR `uid`, `generation` and `namespace`, the `cluster` `uid` and `epoch`, and an optional `expected_owner_uid`) is applied by `SetSourcedComponentIntent`, which orders intents by the source instead of by `spec.generation`, so the CMS generation is independent of the CR generation. Every write records the source as the owner of the record in the `SourceUid`, `SourceGeneration`, `SourceNamespace`, `SourceClusterUid` and `SourceEpoch` columns, with `SourceLastSeenTime` at the commit time. Both stores share one planner (`planIntent` in `pkg/components/store/intent_change.go`; the owner model is in `source_ownership.go`). No wall clock is compared:

| Source of the intent vs recorded owner | Changed intent | Unchanged intent |
| :--- | :--- | :--- |
| R1: no active record (none, or soft-deleted) | Created at generation 1; with `expected_owner_uid` set, rejected (`SOURCE_INCARNATION_MISMATCH`, empty `owner_*` metadata) because the CAS target no longer exists | Same |
| R2: no recorded owner | Adopted, generation + 1 | Adopted: records the owner only |
| R2: owner without a uid (cleared by a fence takeover), same namespace and cluster | Adopted, generation + 1 | Adopted: records the owner only |
| R3: owner object (same uid), generation not older | Applied, generation + 1 | Records a newer CR generation or cluster epoch, else no-op |
| R3: owner object, older generation | Rejected (`STALE_GENERATION`, returned only by `SetComponentIntent`) | No-op |
| R4: another namespace | Rejected (`SOURCE_NAMESPACE_CONFLICT`); the first owner keeps the record | Same |
| R5: same namespace and cluster, another uid, `expected_owner_uid` names the owner uid | Adopted (CAS), generation + 1 | Adopted (CAS): records the owner only |
| R6: same namespace, another uid, otherwise (including a CAS guard from another cluster) | Rejected (`SOURCE_INCARNATION_MISMATCH`) | Same |

Ownership rejections carry metadata `operation`, `owner_namespace`, `owner_uid` and `owner_cluster_uid` (plus `owner_generation` for `STALE_GENERATION`). On `SOURCE_INCARNATION_MISMATCH` the caller proves that its own CR is live (an uncached GET) and retries once with `expected_owner_uid` set to `owner_uid`, which adopts the record under the same `ComponentId`; a stale caller drops its object instead. A GET in one cluster cannot prove an owner in another cluster gone, so CAS works only within the owner's cluster; moving records between clusters is the fence takeover's job. The intent is classified against the live record (`planLiveIntent`) before its dependencies are validated, in the same lock or transaction, so a rejected source gets its own reason even when it also names an unregistered dependency or would close a cycle; accepted intents, including unchanged ones, keep the dependency checks above. Recording only the owner writes no snapshot, generation, state or `UpdateTime`. `SourceLastSeenTime` moves only on owner writes (a create, a change, or a newly recorded source), never on a replay that writes nothing. The Spanner read-only fast path still answers such replays; owner writes and rejections are decided by the read-write transaction. Unsourced intents bypass ownership, keep the generation-ordered behavior above, and leave the recorded owner unchanged. `validate_only` checks the spec and the source fields, not ownership. `ValidateRequestSource` requires the `namespace` to be a DNS-1123 label, an optional `cluster.name` to be one too when set, and the `cluster.uid` to be a canonical (lowercase, hyphenated) UUID, the `kube-system` Namespace UID. Ownership is a correctness guard against stale or duplicate CRs, not a security boundary: the source is asserted by the client.

**Sourced observation & deactivation**: `UpdateObservedGeneration`, `PrepareDeactivation` and `CompleteDeactivation` accept the same optional `source` and apply the fence and the R2–R6 ownership checks, but never return `STALE_GENERATION`. An accepted sourced call writes the owner columns with `SourceLastSeenTime` at the commit time. The owner object keeps the recorded `SourceGeneration`; an adoption (R2 or R5) records `SourceGeneration = 0`, so the adopter's next intent is never rejected as stale. An `UpdateObservedGeneration` that changes no observed generation, state or status (a heartbeat) leaves `UpdateTime`, the etag and the snapshot alone, and writes only the owner columns of a sourced call. Only the target is ownership-checked: dependents that a `cascade: true` `PrepareDeactivation` moves to `DISABLING` skip the per-CR uid check and keep their recorded owner, because CMS changes them on behalf of the target's owner.

**Ownership fence**: CMS accepts component writes from one cluster at a time. Its database outlives any single GKE cluster, so another cluster can reach the same CMS: a rebuilt or disaster-recovery cluster, the new cluster of a migration, or a second environment misconfigured to point at the same database. Ownership (above) records the owner's cluster, so the CRs of a new cluster are rejected (R6), and the same-cluster CAS cannot help across clusters. The fence gives an explicit, reviewable handover: the new cluster declares a higher epoch, takes over the owned components, and adopts them in place. Meanwhile the previous cluster, if it is still running, can no longer change them. This is not multi-cluster support: there is exactly one active writer per CMS.

A takeover moves owned components to the new holder, whose intents adopt them (R2), while the fence (below) rejects intents from the fenced-out cluster. `DeclareClusterEpoch(cluster{uid, name, epoch})` maintains the `ComponentOwnershipFence` row (`FenceId = 1`; `pkg/components/store/ownership_fence.go`, `spanner_ownership_fence.go`). The `uid` must be a lowercase hyphenated UUID (the kube-system Namespace UID) and the `name` a DNS-1123 label; otherwise the call fails with `InvalidArgument`. Like the other mutating RPCs, it is limited to the authorized controller service account (the bridge) or a platform admin; other callers get `PermissionDenied`. The response reports the active cluster and an `outcome`:

| Fence vs declaration | `outcome` or rejection |
| :--- | :--- |
| No fence | `OUTCOME_CREATED`: the caller becomes active (first owner keeps) |
| Same cluster, same epoch | `OUTCOME_REFRESHED`: a heartbeat that writes only `LastSeenTime` and, if it changed, the name, so it takes no write lock on the epoch or uid |
| Same cluster, higher epoch | `OUTCOME_RAISED`: raises the epoch |
| Same cluster, lower epoch | Rejected (`CLUSTER_EPOCH_REGRESSED`), for example after a Git revert |
| Another cluster, higher epoch | `OUTCOME_TAKEOVER` in one transaction: every active component with an owner gets the caller's cluster and epoch and no owner uid, keeping its namespace, so the caller's next intent adopts it (R2) under the same `ComponentId` and the previous holder's intents are rejected (R6); `records_reassigned` counts them |
| Another cluster, equal or lower epoch | Rejected (`CLUSTER_FENCED`); equal epochs from two clusters cannot both win |

The fence is enforced on every sourced intent before the ownership rules, in the same read-write transaction (and the read-only fast path reads it in the same single-use query, as scalar subqueries; because that read is strong, a fence rejection it sees is returned directly, so fenced clusters replaying existing components cost no read-write transaction): the source must come from the active cluster at the fence epoch. Before the first declaration, and for the active cluster at a newer, undeclared epoch, the source gets `CLUSTER_NOT_DECLARED` (declare, then retry); the active cluster at a lower epoch gets `CLUSTER_EPOCH_REGRESSED`; any other cluster gets `CLUSTER_FENCED`, so a fenced cluster cannot create or change components. Enforcement is what makes a takeover safe while the previous cluster is still running: all its sourced writes are rejected, so it cannot compete with the new holder (xmc-bridge then stops writing to CMS and stops actuating: cold standby). Messages name the source cluster by `cluster.name` when it is set, else by its uid. Unsourced intents skip the fence. An admitted, unchanged replay of an older generation of the owner object stays a no-op even at a newer epoch; the recorded epoch moves with the owner's next owner write.

Fence rejections are `FailedPrecondition` with metadata `operation`, `active_cluster_uid`, `active_cluster_name` and `active_epoch`; `CLUSTER_NOT_DECLARED` omits the `active_*` keys when there is no fence row. The takeover updates four columns per owned component in one commit (owner columns only: like any owner-only write it leaves the generation, `UpdateTime` and etag alone, because the owner is not part of the `Component` resource), which counts against Spanner's per-commit mutation limit (80,000): ample for the expected tens to hundreds of components, but the ceiling is about twenty thousand. It sets `SourceLastSeenTime` to its commit time, which starts the adoption grace period of the stale-owner alert. `LastSeenTime` and `DeclaredTime` are required commit timestamps for alerting only; they never move ownership. `Epoch` must be at least 1 (`CK_ComponentOwnershipFence_Epoch`); `FenceId` has no singleton CHECK because it is reserved for partitioned fences.

**Sourced lifecycle mutations**: `UpdateObservedGeneration`, `PrepareDeactivation`, `CompleteDeactivation`, and `ExecuteDeletion` accept an optional `RequestSource` (`checkSourceMutation` in `pkg/components/store/source_ownership.go`). When supplied, the store enforces the cluster fence and ownership rules (R2–R6) in the same transaction before mutating state, recording the caller as owner (`SourceLastSeenTime` at commit time). A non-owner `ExecuteDeletion` fails with `FailedPrecondition` (`SOURCE_INCARNATION_MISMATCH`, `SOURCE_NAMESPACE_CONFLICT`, `CLUSTER_FENCED`, or `CLUSTER_EPOCH_REGRESSED`) and leaves the CMS record intact.

**Ownership & Fence Metrics, Alerts & Runbooks (`pkg/components/store/ownership_metrics.go`)**:

All labels are bounded (no per-component or per-UID label cardinality):

| Metric | Type & Labels | Alert / Meaning |
| :--- | :--- | :--- |
| `xmc_cms_component_owner_adoptions_total` | Counter (`reason=initial\|same_cluster_recreate\|epoch_takeover\|unsourced_claim`) | Committed owner adoptions across store writes. Alert/notify on `reason="epoch_takeover"` (confirm expected DR takeover). |
| `xmc_cms_component_source_conflicts_total` | Counter (`reason=STALE_GENERATION\|SOURCE_INCARNATION_MISMATCH\|SOURCE_NAMESPACE_CONFLICT\|CLUSTER_FENCED\|CLUSTER_EPOCH_REGRESSED\|CLUSTER_NOT_DECLARED`) | Store ownership and cluster fence rejections. Page on `CLUSTER_EPOCH_REGRESSED` or sustained `CLUSTER_FENCED` at an equal epoch (split-brain or cluster restore). |
| `xmc_cms_cluster_fence_epoch` | Gauge | Active cluster ownership fence epoch declared in CMS. |
| `xmc_cms_component_owner_stale` | Gauge | Active owned components whose owner UID is unassigned after a fence takeover (`SourceUid IS NULL`) or whose `SourceLastSeenTime` is older than 15 minutes (`OwnerStaleThreshold`), updated on `DeclareClusterEpoch`. Alert when `> 0` for 30 minutes (orphaned component owner). `SourceLastSeenTime` advances only on owner writes, so a component whose CR is unchanged and already synced can also count after 15 minutes; confirm its CR before acting. The gauge is per replica and only as fresh as that replica's last declaration, so aggregate with `max`. |

Operational runbooks:
- **DR failover**: Commit `componentsDeployment.ownershipEpoch: <active+1>` in the DR cluster's GitOps values overlay. Once synced, the DR `xmc-bridge` calls `DeclareClusterEpoch` (`OUTCOME_TAKEOVER`), reassigning active owned components and adopting them under their existing `ComponentId`. Any returning old primary cluster is fenced (`CLUSTER_FENCED`). The DR `xmc-bridge` must call CMS as the controller service account the experiment state server authorizes: the chart uses `gcpServiceAccount` both as `ess_service_account` and as the Workload Identity annotation on `xmc-bridge-sa`. Any other caller gets `PermissionDenied` and no takeover happens.
- **Failback**: Bump `componentsDeployment.ownershipEpoch` on the primary cluster to `<dr_epoch+1>` in Git.
- **Split-brain or cluster restore (`CLUSTER_FENCED`)**: Confirm which cluster is the intended writer; the unintended cluster remains in cold standby until decommissioned.
- **Orphaned owner (`xmc_cms_component_owner_stale > 0`)**: Recreate the missing CR in the active cluster (auto-adopts via R2/R5) or issue an unsourced `ExecuteDeletion` to retire the record.
- **Namespace conflict (`SOURCE_NAMESPACE_CONFLICT`)**: Delete the misplaced CR; `Component` CRs belong only in the configured `componentsDeployment.namespace`.
- **Epoch regressed (`CLUSTER_EPOCH_REGRESSED`)**: Restore the higher `componentsDeployment.ownershipEpoch` in Git.

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

If a prospective update would introduce a self-cycle or transitive cycle, the operation is rejected immediately with gRPC `codes.FailedPrecondition`, a `dependency cycle detected: a -> b -> a` message, and an ErrorInfo with reason `DEPENDENCY_CYCLE` (`store.ReasonDependencyCycle`) and metadata `operation` (`SetComponentIntent`), `component`, and `cycle` (the canonical names on the cycle, comma-separated).

---

### 3. Cascading Deactivation & Deletion Engine (`pkg/components/store/deactivation_engine.go`)

Components cannot be deactivated or deleted arbitrarily if other active components depend on them. The deactivation engine enforces lifecycle safety boundaries.

#### Deactivation Safety & Cascading
- `EvaluateDeactivationSafety(components map[string]*ComponentRecord, targetName string, requestCascade bool, defaultDrain time.Duration) (*PrepareDeactivationResult, error)`
  - **Blocking Mode (`requestCascade = false`)**: If non-deleted, non-disabled dependents exist, deactivation is blocked with `codes.FailedPrecondition` and an ErrorInfo with reason `ACTIVE_DEPENDENTS_EXIST` (`store.ReasonActiveDependentsExist`) and metadata `operation` (`PrepareDeactivation`), `component`, and `dependents` (comma-separated canonical names), and returns the list of blocking dependents. `ValidateDeletionSafety` rejects a non-cascading (`DELETION_POLICY_ORPHAN`) `ExecuteDeletion` with active dependents the same way, with `operation` `ExecuteDeletion`.
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

### 6. Dynamic Drift Detection & Warning Propagation (`internal/experiment/driftdetection.go`)

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
              (GetExperiment, GetExperimentStatus)
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
   - Drift evaluation executes exclusively within top-level read RPCs (`GetExperiment`, `GetExperimentStatus`) using an independent read-only transaction (`ReadOnlyTransaction`) in `FetchDriftedComponents`.
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
3. **Placement Gating**: Actuator blocks placement and requeues the task if required components are missing (hydration lag), disabled, or unhealthy (`Ready=False`/`Unknown`).
4. **Drift Warning**: If a component's generation drifts past the snapshot lock, Actuator allows placement but logs a warning and annotates the JobSet with `xmc.dev/component-drift-warning`.
5. **Configuration Injection**: Actuator injects plain environment variables (`status.access.envVars`) into all primary and init containers across all replicated jobs.
