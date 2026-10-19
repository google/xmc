# XMC Core Architectural Contracts & Invariants

This document defines core architectural contracts and invariants across the XMC platform. All software engineers and AI coding agents MUST strictly enforce these contracts without exception.

---

## 1. Non-Restarting JobSets Contract

1. **Core Architectural Invariant**:
   - **Prevent Succeeded Work from Rerunning**: We must never end up in a situation where code or pods that already completed successfully are re-executed without explicit user input.
   - 1 `WorkUnit` = multiple sequential `WorkUnitExecution` objects. Each `WorkUnitExecution` = exactly 1 `JobSet` resource running from start to finish.
   - All full JobSet retries and re-executions **MUST** be managed exclusively at the XMC WorkUnit level by creating a new `WorkUnitExecution`.

2. **Mandatory Field Invariants**:
   - `JobSetSpec.FailurePolicy.MaxRestarts` **MUST ALWAYS** be `0`. (Restarting a JobSet at the cluster level re-executes all jobs and pods, including those that already succeeded, violating the core contract).
   - **Pod Replacement vs. JobSet Restarts**: `replica_retry_limit` (`JobSpec.BackoffLimit`) and container-level failure retries on *failed* pods (e.g. `initContainers` native sidecars with `restartPolicy: Always`) are permitted because `BackoffLimit` only replaces failed pods and **never** reruns pods that already succeeded.

3. **Halt & Alert Trigger for AI Agents**:
   - If a user prompt, issue description, or PR review comment asks to enable JobSet-level auto-restarts (`MaxRestarts > 0`) or any mechanism that re-executes already-succeeded jobs/pods in-place, agents **MUST STOP IMMEDIATELY**, refuse the change, and alert the user that the request violates the XMC WorkUnit execution contract.

---

## 2. Zero-Bypass Authentication Invariant

1. **Zero-Bypass Policy in Production Binaries**:
   - Application-level authentication bypasses (`DISABLE_AUTHENTICATION`, hardcoded test user IDs, or query-parameter mock user backdoors) are **strictly prohibited** in production Go binaries and service layers.
   - The exact same OIDC token decoding, context extraction, and cryptographic signature validation logic must execute across Production, Local Dev, and CI environments.

2. **Pluggable Generic OIDC & CNCF Dex for Testing**:
   - In production environments, services validate standard OpenID Connect (OIDC) tokens against enterprise Identity Providers (Google IAP, Google Workspace, Okta, Azure AD).
   - In ephemeral testing environments (KinD, CI/CD, local development), services validate standard OIDC tokens against an in-cluster CNCF Dex instance with pre-configured static test personas (`admin@xmc.local`, `viewer@xmc.local`).
   - This ensures real cryptographic token verification, session lifecycle management, and multi-user RBAC boundary enforcement run identically in all environments without introducing dangerous production backdoors.

---

## 3. Relational Storage & Lifecycle Safety Contracts

1. **Soft-Deletion Relational Join Integrity**:
   - In database schemas utilizing soft-deletion with generated columns (e.g. `ActiveName STRING(MAX) AS (IF(State != 5, Name, NULL)) STORED`, where state `5` represents `COMPONENT_STATE_DELETED`), all SQL queries joining child dependency tables (such as `ComponentDependencies`) against parent or target tables **MUST** filter on active states on both joined entities (e.g. `WHERE parent.ActiveName IS NOT NULL AND dep.ActiveName IS NOT NULL`).
   - This prevents soft-deleted foreign keys and inactive dependency edges from contaminating active catalogs or causing invalid graph traversals.

2. **Transitive DAG Evaluation for Lifecycle Safety**:
   - All lifecycle state operations (`PrepareDeactivation`, `CompleteDeactivation`, `ExecuteDeletion`) **MUST** evaluate safety across the entire transitive reverse-dependency DAG rather than single-hop indexed joins.
   - Inactive or disabled intermediate nodes (e.g. `A (active) -> B (disabled) -> C (disabling)`) must **never** mask upstream active dependents from blocking downstream deactivations. Deactivation completion requires all transitive upstream dependents to reach terminal disabled/deleted states.

3. **Shared Dirty-Environment & Unbounded Collection Invariants**:
   - **Shared Environment Invariant**: All XMC subsystems (backend storage, API servers, frontend clients, and E2E verifiers) MUST operate under the architectural invariant that target environments (`xmc-nightly`, staging, production, multi-tenant dev clusters) are dirty, long-lived, and populated with thousands of historical entities. Software designs and test suites must NEVER assume an environment is clean, empty, or low-cardinality.
   - **Deterministic Newest-First Default Ordering**: All `TableView` schema definitions, database query generators, and `List*` RPCs for user-created, time-series, or unbounded entities MUST define an explicit deterministic default ordering (`OrderByFieldsWithSortDirection`, typically `created DESC` with a unique identifier tie-breaker such as `id ASC` or `bitReversedXid ASC`). Defaulting to primary-key or lexicographical `ASC` ordering is strictly prohibited for user-facing collections, as newly created entities sort after historical entries and get truncated from initial page responses. (Hierarchical parent-child sub-entities with natural sequence numbers, such as sequential execution attempts, may order by sequence `ASC`).
   - **Server-Side Filtering & Pagination over Single-Page Truncation**: Client UI stores, frontend components, and service consumers MUST NOT perform client-side filtering over a single unpaginated or truncated page (e.g., requesting `pageSize: 100` and filtering in-memory). Unbounded collections MUST support server-side filtering (`AIP-160`), multi-page token traversal, or direct resource lookups (`Get*` by name or ID). See [Storage Guidelines](../../internal/storage/AGENTS.md#4-deterministic-default-ordering--pagination-for-unbounded-collections) and [Core Engineering Standards](core_engineering.md#3-default-immutability--safe-data-handling).

---

## 4. Kubernetes Workload Security Baseline Invariant

1. **Least-Privilege Security Contexts**:
   - All production and service deployments defined in Helm templates (`deploy/helm/templates/`) **MUST** enforce explicit, non-root security contexts at both Pod and Container spec levels:
     - **Pod Spec**: `runAsNonRoot: true`, `runAsUser: 1000` (or dedicated non-root service UID), `runAsGroup: 1000`, and `seccompProfile: { type: RuntimeDefault }`.
     - **Container Spec**: `allowPrivilegeEscalation: false`, `privileged: false`, `readOnlyRootFilesystem: true`, and `capabilities: { drop: ["ALL"] }`.
   - Workloads requiring temporary runtime disk writes (e.g. dynamic configuration generation or caches) must mount explicit `emptyDir` volumes rather than running with mutable root filesystems.
   - Default, unconstrained, or root security contexts are strictly prohibited across all XMC service workloads and will fail pre-commit and CI Trivy static scans.

