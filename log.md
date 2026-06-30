# Design Audit Log: XMC Workload Pause/Resume

This log tracks the iterations, reviewer feedback, and architectural decisions for the XMC workload pause/resume mechanism.

---

## Iteration 1: Initial Review & Architectural Pivot (2026-06-30)

### 1. Reviewer Feedback Summary

#### Architect Reviewer (FATAL)
* **Data Loss on Capped Redis Stream:** The current `xmc-tasks-stream` is capped at 10,000 messages. During a 24+ hour pause, enqueuing thousands of tasks will exceed this limit, leading to silent trimming and data loss.
* **Token Expiration (DLQ Dump):** Task payloads contain GCP OIDC and JWT tokens with 1-hour lifetimes. If tasks are buffered in Redis for 24+ hours, validation will fail on resume, and all tasks will be dumped to the DLQ.
* **Thundering Herd:** Actuators will concurrently consume thousands of unpaused tasks, overloading Kueue/Kubernetes control plane.
* **Startup Race Condition:** A race condition exists between reading Spanner state and establishing the Redis stream offset.

#### QA Reviewer (FATAL)
* **OOM Risk in Redis:** Accumulating thousands of tasks in memory poses an OOM risk for Redis.
* **Missing Verification Plan:** No verification plan or security section exists to validate coding guidelines.
* **Unfinished Sections:** Document contains multiple `// TODO` placeholders.

#### Developer Reviewer (PRAGMATISM)
* **Overengineered Broadcast:** The dedicated Redis stream `xmc-pause-stream` for broadcasting pause events is overengineered. Polling Spanner every 5-10 seconds is acceptable and simpler.
* **Non-Standard Single-Row Schema:** `SystemConfig` table schema using `PRIMARY KEY ()` is non-standard. Suggests `PRIMARY KEY (Id)` with `Id = 1`.

---

### 2. Architectural Decisions & Pivot Rationale

To address all fatal flaws, we are **pivoting the task buffering architecture** from transient Redis-based buffering to durable Spanner-based status tracking:

1. **Durable Task Buffering in Spanner:**
   * When `ActuatorSchedulingPaused` is active, the Experiment State Server (ESS) will **not** publish tasks to the Redis `xmc-tasks-stream`.
   * ESS will create/update the WorkUnit in Spanner and set its state to `PAUSED`.
   * This prevents Redis from filling up, eliminating OOM and stream-trimming data loss risks.

2. **Draining and Throttling on Resume:**
   * When the admin unpauses, a background draining process (run inside the Actuator or Reconciler) will query Spanner for WorkUnits in the `PAUSED` state.
   * These tasks will be enqueued into the Redis stream progressively using a **token bucket rate limiter** to avoid thundering herd on Kueue.
   * Credentials (OIDC/JWT) will be generated **at the time of draining/publishing**, resolving the credential expiration issue.

3. **Simplified State Propagation:**
   * Remove the Redis broadcast stream `xmc-pause-stream`.
   * Actuators will poll the Spanner `SystemConfig` table every 5-10 seconds. This is simple, robust, and avoids startup race conditions.

4. **Schema Standardization:**
   * Modify the `SystemConfig` table to use `PRIMARY KEY (Id)` with `Id = 1` enforced.

5. **Completing the Document:**
   * Add a complete Verification Plan with a Security Section.
   * Resolve all TODOs for UI integration, health status endpoints, and limitations.

---

## Iteration 2: Re-Review & Final Approval (2026-06-30)

### 1. Reviewer Feedback Summary

#### Architect Reviewer (LGTM)
* **Status:** Approved.
* **Suggestions:** Reset tasks stuck in `SCHEDULING` for more than 1 hour to `PAUSED` to handle transition-phase expired tasks (resolved: added to draining query).

#### QA Reviewer (LGTM)
* **Status:** Approved.
* **Suggestions:** Multi-replica coordination of draining workers across Reconcilers to avoid duplicate enqueues (resolved: specified using Kubernetes leader election so only the leader replica runs the draining loop).

#### Developer Reviewer (PRAGMATISM)
* **Concern:** Dual control gates (buffering in database AND pausing consumer) might be redundant.
* **Resolution:** Kept both. Documented justification that they serve non-overlapping functions: Ingress buffering solves data durability and Redis OOM; Egress pausing immediately halts traffic to degraded Kueue control planes.

---

### 2. Final Architectural Adjustments

1. **Leader Election for Reconciler Draining:** Added Kubernetes leader election to ensure only a single Reconciler replica drains the database to Redis.
2. **Transition-Phase Recovery:** The draining worker now automatically scans for and recovers tasks stuck in `SCHEDULING` for >1 hour.
3. **Control Gate Justification:** Documented the specific roles of Ingress vs. Egress pause gates.

---

## Iteration 3: Task Payload Buffering & Integrity (2026-06-30)

### 1. Architectural Decisions & Rationale

During final design validation, we identified a critical data loss vulnerability in Iteration 2's design:
* **Information Loss on State Updates:** If scheduling is paused and a user requests a `DELETE` or `RESTART` action, simply marking the WorkUnit state as `PAUSED` in Spanner would erase the specific action type. The draining worker would have no way of knowing whether it should enqueue a `CREATE`, `DELETE`, or `RESTART` task to Redis.
* **Corrupting Active States:** Overwriting the state to `PAUSED` for an active job undergoing restart would corrupt the WorkUnit status in the database.

To resolve this, we introduced a dedicated task buffer table:

1. **`PausedTasks` Buffer Table:**
   * Introduced a new `PausedTasks` table in Spanner to store the raw serialized `ActuatorPayload` (which contains the specific `Action` type, target, and tracing metadata).
   * ESS writes the WorkUnit state in `WorkUnits` to `PAUSED` (purely for UI visibility) and concurrently buffers the task payload in `PausedTasks` in a single atomic transaction.
2. **Draining from `PausedTasks`:**
   * The unpausing draining worker now queries `PausedTasks` ordered by `CreateTime ASC` to preserve task ordering.
   * It reconstructs the tasks, signs them with fresh OIDC/JWT credentials, publishes them to Redis, and deletes the buffered rows upon success.

---

## Iteration 4: Architectural & QA Edge-Case Hardening (2026-06-30)

### 1. Reviewer Feedback Summary

#### Architect Reviewer (FATAL)
* **Invalid Spanner Interleave PK:** `PausedTasks`'s primary key `PRIMARY KEY (Xid, WorkUnitId, TaskId)` was invalid because it did not match the parent `WorkUnits` primary key `PRIMARY KEY (BitReversedXid, WorkUnitId)`. Spanner interleave requires exact prefix matching.
* **Resumption Queue Backpressure Omission:** Draining without backpressure could overflow Redis (stream size limit 10,000) or cause task tokens to expire before the Actuator can process them.
* **SystemConfig Lock Contention:** ESS reads `SystemConfig` inside read-write transactions, acquiring shared read locks on the single configuration row, causing lock contention under high load.
* **Secondary Index Omission:** Sorting by `CreateTime ASC` during draining without an index forces an expensive Spanner full table scan and sort.
* **Non-Idempotent Restart Actions:** Leader failover mid-drain could cause double restarts in the cluster.

#### QA Reviewer (FATAL)
* **Transition-Phase Expiration Bug:** A task that expires in Redis after unpausing will go to the DLQ and remain stuck in `SCHEDULING` indefinitely because the transition-phase recovery only ran once on unpause.
* **Draining Worker memory OOM and DB Transaction Timeout:** Loading all task rows from Spanner in a single transaction during draining risks memory OOM and Spanner session timeouts.
* **Redis Eviction Risks:** Shared Redis memory growth could silently evict task stream keys under default LRU policies.
* **Design Gaps:** Missing audit logging schemas, BFF authorization translations, and verification plan cases for leader failovers, migrations, and DLQ monitoring expectations.

#### Developer Reviewer (LGTM)
* Suggested dynamic rate-limiting configured via Spanner.

---

### 2. Architectural Decisions & Rationale

We applied comprehensive updates in `dd.md` to harden the design:

1.  **Corrected Spanner Schema:** Updated `PausedTasks` to use `PRIMARY KEY (BitReversedXid, WorkUnitId, TaskId)` to restore interleaved parent-child key integrity. Added secondary index `PausedTasksByCreateTime` to optimize FIFO draining queries.
2.  **Snapshot Reads for Configuration:** ESS now reads `SystemConfig` using lock-free **Snapshot Reads (Read-Only Transactions)**, resolving ESS lock contention.
3.  **Draining Paging and Backpressure:** The draining worker now reads from Spanner in **paginated batches of 100 rows**. Before processing the next batch, it queries Redis stream size; if backlog exceeds **1,000 tasks**, it pauses to apply backpressure.
4.  **Continuous Stuck Task Recovery Loop:** Replaced the one-off unpause recovery with a **continuous 10-minute background Reconciler loop** to reset and re-buffer any WorkUnits stuck in `SCHEDULING` for >1 hour (handling late-expiring tasks).
5.  **Dedicated Redis Instance:** Specified a dedicated Redis instance with `noeviction` policy to prevent silent data loss.
6.  **Kubernetes Annotation-based Deduplication:** Actuator writes `IdempotencyKey` as an annotation on K8s JobSets, verifying it before executing mutations to guarantee absolute idempotency for non-idempotent actions (like `RestartAction`) on leader failovers.
7.  **Audit Logs & BFF Integration:** Defined the structured audit log schema and BFF OIDC token exchange translation.
8.  **Expanded Verification Plan:** Added test cases for leader election failover, pagination, backpressure, migration schema testing, and operator DLQ expectations.
9.  **Dynamic Rate Limiting:** Added `DrainingRateLimitCount` column to `SystemConfig` to allow live rate limit updates.

---

## Iteration 5: Final Review & Dynamic Config API (2026-06-30)

### 1. Reviewer Feedback Summary

#### Developer Reviewer (NOT LGTM)
* **Redundant Control Gates:** Argued that the Egress pause (Actuator consumer halt) is redundant complexity.
* **Missing Config API Endpoint:** Pointed out that while dynamic rate limiting is in Spanner, there was no PATCH API mapping to update it (forcing manual SQL).

#### Architect Reviewer (LGTM)
* Approved the Iteration 4 updates.

#### QA Reviewer (LGTM)
* Approved the Iteration 4 updates.

---

### 2. Architectural Decisions & Rationale

We resolved the developer reviewer's feedback:

1.  **Justification of Egress Pause Gate:** Re-emphasized that the primary operational goal is *immediate* traffic halt. If we only pause the Ingress (ESS), the Actuator will continue draining up to 10,000 tasks already in the Redis stream, which fails the immediate mitigation requirement.
2.  **Unified Configuration Update Endpoint:** Renamed the PATCH endpoint to `/api/admin/scheduling_config` and expanded it to support updating both `paused` (boolean) and `draining_rate_limit_count` (integer) dynamically, updating all references in the document.

---

## Iteration 6: System Simplification & Stateless Consumer Reversion (2026-06-30)

### 1. Architectural Decisions & Rationale

Based on critical course-corrections, we restructured the workload pause/resume mechanism to maximize simplicity and eliminate unnecessary state management in the Actuator:

1.  **Reverted Actuator Changes:** Removed the Egress-level pause completely. The Actuator remains a simple, stateless Redis consumer utilizing the blocking `XREAD` loop. It does not poll Spanner, maintain a local cache, or require library modifications.
2.  **Selective Operation Pausing:** The scheduling pause is restricted to **Create** and **Restart** actions (which create/recreate resources). **Delete** actions bypass the pause and are enqueued directly to Redis to ensure administrators can clean up malfunctioning experiments during an incident.
3.  **Minimized Spanner Storage (`ActionBytes`):** Instead of serializing the entire task payload (which duplicates the `WorkUnit` proto already stored in Spanner), `PausedTasks` only stores the serialized `actuatortaskpb.Action` proto (`ActionBytes`) along with task metadata. On unpausing, the Reconciler draining worker queries `WorkUnits` to fetch the latest state of the WorkUnit and reconstructs the payload dynamically.
4.  **Renamed Configuration Table:** Changed `SystemConfig` to `SystemSettings` to represent global settings.
5.  **Removed Dynamic Rate Limit Persistency:** Removed `DrainingRateLimitCount` from the database and PATCH API, reverting to environment-variable based configuration (or Helm flags) to keep the database schema clean.
6.  **Justified Buffer Table (Rejected state-only reconstruction):** Added architectural justification explaining why we cannot reconstruct task events from the `WorkUnits` table alone: a `Restart` operation is a transient trigger that spawns a delete-then-create sequence and is not represented by any static database state in `WorkUnits`.
7.  **Removed Misleading Terminology:** Cleaned up all "ingress" and "egress" terminology throughout the design document.

---

## Iteration 7: Transaction Boundary Optimization & Transition Edge-Case Hardening (2026-06-30)

### 1. Architectural Decisions & Rationale

We addressed critical edge cases and transaction anti-patterns identified by the reviewers:

1.  **Optimized Spanner Transaction Boundaries:** Split the draining process into three phases (Fetch, Publish, Commit). Generating token credentials and enqueuing to Redis are now performed *outside* the Spanner read-write transaction. This minimizes database lock hold times to milliseconds, preventing lock contention and abort storms.
2.  **Mitigated Transition Race Condition (Orphaned Tasks):** Extended the periodic Stuck Task Recovery loop to scan for any WorkUnits in the `PAUSED` state when the system is unpaused (representing transition orphans) and publish them directly to Redis, preventing them from getting stuck.
3.  **Prevented Cancelled Job Resurrection:** The draining worker now checks the parent `WorkUnit` state and discards buffered tasks if their WorkUnit is in a terminal state (`CANCELLED`, `STOPPED`).
4.  **Recovered Stuck Bypassed Deletes:** Extended the recovery loop to reset and re-enqueue delete operations stuck in `STOPPING` for >1 hour.
5.  **Redis Write Error Propagation:** Enforced synchronous 500 error returns in ESS if Redis publishing fails, ensuring callers can retry.
6.  **Cleaned Up Documentation:** Removed the outdated caching NFR (replaced with lock-free snapshot reads) and the contradictory "Ingress Pause Only" rejected alternative.
7.  **Documented Kueue Pause Rejection:** Added a rejected alternative explaining why pausing Kueue queues directly via Kubernetes API was rejected (to avoid circular dependency on control plane health during an API outage).

---

## Iteration 8: Native Proto Storage, Actuator Broadcast Pause & Lifecycle Conflict Handling (2026-06-30)

### 1. Architectural Decisions & Rationale

Based on direct user requirements, we introduced the following refinements and restored critical real-time features:

1.  **Native Proto Column Type:** Changed the type of the `Action` column in `PausedTasks` from raw `BYTES(MAX)` (`ActionBytes`) to native `cloud.xmanager.v1.Action` proto. Registered the proto message in the Spanner schema DDL's `CREATE PROTO BUNDLE` block, improving database schema type safety.
2.  **Restored Actuator Broadcast Pause (`xmc-pause-stream`):** Re-implemented the real-time Actuator pause gate. The Health Service publishes `PAUSE`/`RESUME` commands to the Redis broadcast stream `xmc-pause-stream` on configuration changes. The Actuator queries Spanner once at startup and then blocks on the Redis broadcast stream to immediately stop/resume main task consumption during incidents, achieving immediate mitigation without database polling.
3.  **Conflict Resolution for Overlapping Operations:** Documented the lifecycle behavior when a `Delete` command is issued on a WorkUnit that has a pending `Restart` buffered in Spanner. The bypassed `DELETE` task cleans up resources immediately. On unpause, the draining worker validates the terminal state of the WorkUnit and discards the buffered `RESTART` action, avoiding job resurrection.
4.  **WorkUnit State Renaming:** Updated the user-facing visibility state of paused WorkUnits from `PAUSED` to `GLOBAL_PAUSE`.
5.  **Refined Resumption Backpressure Signal:** Swapped raw `XLEN` queue size queries with Redis consumer group `lag + pending` unacknowledged count evaluations, providing a highly accurate indicator of unprocessed control plane backlog.
6.  **Skipped Audit Logging:** Removed all audit logging definitions and verification tests.
7.  **Removed Alerting & Capping Overrides:** Removed the dedicated Redis configuration and memory limits configuration guidelines, and removed the corresponding eviction prevention tests.

---

## Iteration 9: Draining Worker Architecture Shift to Health Service (2026-06-30)

### 1. Architectural Decisions & Rationale

We shifted the resumption draining worker and the periodic stuck task recovery loops from the Kubernetes Reconciler to the **Health Service** container:

1. **Separation of Concerns:** Keep the Reconciler focused strictly on Kubernetes resource synchronization (JobSets/Workloads) and offload custom database/Redis enqueuing.
2. **Single-Replica Deployment & No Leader Election:** To simplify coordination, the Health Service is deployed as a single instance (`replicas: 1`). This eliminates the requirement for complex Kubernetes leader election during the initial phase.
3. **Trigger-with-Fallback Execution:**
   - **Internal Trigger (No Polling):** An internal Go channel (`drainSignal`) immediately wakes up the background goroutine to start draining the `PausedTasks` table when the admin API toggles `paused = false`.
   - **Startup Recovery:** On container boot, if Spanner indicates the system is unpaused, the Health Service automatically sends a signal to `drainSignal` to drain any tasks that remained in `PausedTasks` due to a crash.
   - **Periodic Heartbeat:** A slow periodic ticker (e.g., every 5 minutes) triggers a check of `PausedTasks` as a fallback to recover from any transient errors or missed signals, eliminating database polling load during normal operations.
4. **Consistency:** Renamed all remaining Reconciler draining worker references to the Health Service draining worker.

## Iteration 9: Revision 2 & 3 (Security Validation, ESS Sync & Final Approval)

### 1. Reviewer Feedback Summary

#### Architect Reviewer (FATAL - Revision 2)
* **ESS Startup Race Condition:** A race condition exists at ESS startup where a pause event could be published and committed concurrently with ESS boot, resulting in ESS missing the event and enqueuing tasks despite the pause.
* **Actuator Broadcast Stream Redundancy:** Confirmed that the broadcast stream is only for ESS instances to sync cache, and Actuator remains stateless (resolved typo).
* **Multi-Replica rolling update safety:** rolling updates could briefly spawn a second replica, causing dual draining.

#### QA Reviewer (FATAL - Revision 2)
* **Incomplete Security Validation:** Missing explicit test scenarios for input validation, session management scope checks, and BFF validation (CSRF token verification, custom cookies/headers, CORS policy limits, and secure HTTP headers).
* **Redis configuration parameters:** Recommended documenting Redis isolation, dedicated instance guidelines, and specific `maxmemory` guidelines.

#### Developer Reviewer (LGTM)
* Approved Revision 2 with no comments.

---

### 2. Architectural Decisions & Final Adjustments

We resolved all reviewer concerns in Revision 3, 4, and 5:

1. **Reversed ESS Startup Order:** ESS now queries the Redis stream offset (using `XREVRANGE`) *first*, performs the Spanner synchronous settings read *second*, and spawns the background `XREAD` listener *third*. This mathematically eliminates the startup race condition.
2. **Kubernetes Rollout Strategy 'Recreate':** Health Service Helm templates configure `strategy.type: Recreate` on the deployment to guarantee a single replica is active even during updates, avoiding leader election.
3. **Completed Security Verification Plan:** Expanded the Verification Plan to explicitly detail Input Validation, Session Management scope verification, and BFF validation (CSRF, CORS, secure HTTP headers nosniff/DENY).
4. **Redis Infrastructure Guidelines:** Documented Redis isolation requirements, `maxmemory-policy noeviction` enforcement, and a recommended `maxmemory 2GB` limit.
5. **Idempotency Updates:** Actuator `CreateWorkUnit` handles GKE `AlreadyExists` errors gracefully, treating them as successful when matching the target execution ID.
6. **Corrected Table References:** Unified all occurrences of `SystemConfig` to `SystemSettings`.
7. **Transition Orphan DB Verification:** Added database row deletion verification to the Verification Plan for transition orphans.

### 3. Reviewer Final Approvals (Revision 5)
* **Architect Reviewer:** LGTM
* **QA Reviewer:** LGTM
* **Developer Reviewer:** LGTM

