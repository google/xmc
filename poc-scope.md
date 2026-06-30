# Proof of Concept Scope: XMC Workload Pause/Resume

This document outlines the scope, simplifications, and step-by-step implementation plan for the Pause/Resume prototype. The goal is to build a functional Proof of Concept (POC) demonstrating Spanner-backed task buffering and rate-limited resumption, executing entirely within the **Experiment State Server (ESS)**.

---

## 1. Core Simplifications for the POC

To deliver a working prototype rapidly and validate the core database-buffering model, we apply the following simplifications:

1. **No Health Service:** All APIs, background loops, and configuration state changes are hosted directly inside the **ESS** codebase.
2. **No Redis Broadcast Stream:** ESS instances will poll the Spanner `SystemSettings` table every 5 seconds to update their local pause state cache, eliminating the need to configure or listen to a Redis configuration stream.
3. **Redis-Based Leader Election (`SET NX`):** Because ESS runs with multiple replicas, we must prevent concurrent draining. We will implement a lightweight leader election mechanism using Redis atomic `SET NX` locks. Only the replica holding the Redis leader lock will execute the draining worker and stuck task recovery loops.
4. **Mocked/Simplified UI:** State checks will be verified using `curl` requests against the ESS admin endpoints and direct queries to Spanner and Redis.

---

## 2. Spanner Schema Modifications for POC

We will deploy two tables in Spanner (reusing the existing Redis instance for leader coordination):

```sql
-- 1. Global Pause Settings Table
CREATE TABLE SystemSettings (
  Id INT64 NOT NULL,
  ActuatorSchedulingPaused BOOL NOT NULL DEFAULT (FALSE)
) PRIMARY KEY (Id);

-- 2. Buffer Table for Paused Task Actions
CREATE TABLE PausedTasks (
  BitReversedXid INT64 NOT NULL,
  WorkUnitId INT64 NOT NULL,
  TaskId STRING(MAX) NOT NULL,
  Target STRING(MAX) NOT NULL,
  ActionProto BYTES(MAX) NOT NULL, -- Simplified to BYTES for POC to avoid registering proto bundles
  OperationName STRING(MAX) NOT NULL,
  TraceId STRING(MAX) NOT NULL,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE)
) PRIMARY KEY (BitReversedXid, WorkUnitId, TaskId),
  INTERLEAVE IN PARENT WorkUnits ON DELETE CASCADE;

CREATE INDEX PausedTasksByCreateTime ON PausedTasks (CreateTime);
```

---

## 3. ESS Component Designs

### A. State Toggle Endpoint & Polling Loop
* **API:** Expose `PATCH /api/admin/scheduling_pause` on the ESS HTTP server. It updates `SystemSettings` (with `Id = 1`) in a Spanner Read-Write transaction.
* **Polling Goroutine:** Every ESS replica spawns a background ticker goroutine querying `SystemSettings` (using lock-free Spanner snapshot reads) every 5 seconds. It updates the in-memory boolean `local_paused`.

### B. Redis Leader Coordinator & Architectural Rationale

* **Architectural Rationale for Single-Worker Draining:**
  Google Cloud Spanner **does not support `SKIP LOCKED` or `NOWAIT` syntax** inside SQL queries. If multiple ESS replicas were to scan and claim tasks concurrently using standard Read-Write transactions:
  1. Concurrent replicas would acquire Shared Read Locks on the scanned rows.
  2. During the subsequent delete/claim update phase, they would try to upgrade them to Exclusive Write Locks.
  3. This inevitably leads to database deadlocks and high **transaction abort storms**, degrading Spanner performance.
  Therefore, to prevent lock contention and guarantee transaction safety during draining, task draining must be serialized and run on a **single leader replica**.

* **Redis Leader Election Loop:**
  * **Frequency:** Every ESS replica runs a loop every 10 seconds attempting to acquire or renew a lock in Redis.
  * **Acquisition Command:**
    ```
    SET ess-draining-leader-lock <pod-name> NX PX 30000
    ```
    * If the command succeeds (returns `OK`), the replica is the leader. It renews the lock every 10 seconds.
    * If it fails, the replica is a follower.
  * **State Transition:** When a replica wins the Redis leader lock, it spawns the background Draining and Stuck Task Recovery goroutines. If it loses the lock (or fails to renew it), it cancels their context immediately.

### C. Draining Goroutine & Stuck Task Loop
* Only executes on the leader instance.
* Runs the page-draining logic in batches of 100 from `PausedTasks` to `xmc-tasks-stream` in Redis outside Spanner transaction boundaries.
* Runs the 10-minute Stuck Task Recovery Loop.

---

## 4. Step-by-Step Implementation Plan

### Step 1: Database Migration Setup
* Create Spanner DDL migration files to create `SystemSettings` and `PausedTasks` tables. (Releases/leases table is omitted).

### Step 2: Implement the Redis Leader Coordinator (Go)
* Create `pkg/lease/coordinator.go` using `go-redis` client to execute the `SET NX` locking loop every 10 seconds.

### Step 3: Implement the Pause API & ESS Polling Loop
* Register the `PATCH` handler in ESS routing.
* Implement the background ticker in ESS startup to update the local cache.

### Step 4: Implement Ingress Gating & Database Buffering
* Modify ESS task submission handlers (`CreateWorkUnit`, `RestartWorkUnit`) to intercept tasks when `local_paused == true`.
* **Atomic Spanner Transaction:** In a single Spanner Read-Write transaction, ESS must:
  1. Write the task payload to the `PausedTasks` buffer table.
  2. Update the parent `WorkUnit` state in the `WorkUnits` table to `GLOBAL_PAUSE` (to evaluate user-facing visibility).

### Step 5: Implement the Resumption Draining Loop
* Implement the batch draining goroutine triggered via Go channel signals (`drainSignal`) and periodic checks when unpaused.

---

## 6. Verification Plan

1. **Verify State Toggle:** `curl -X PATCH -d '{"paused": true}' http://<ess-ip>/api/admin/scheduling_pause`. Verify Spanner table updates.
2. **Verify Polling Sync:** Confirm via logs that all ESS replicas sync the pause state within 5 seconds.
3. **Verify Ingress Pause Gating & Status Update:** Submit a `Create` request. Confirm it is written to Spanner `PausedTasks`, is **not** published to Redis, and the parent WorkUnit state in the `WorkUnits` table is updated to `GLOBAL_PAUSE`.
4. **Verify Delete Bypass:** Submit a `Delete` request. Confirm it bypasses the pause and goes directly to Redis.
5. **Verify Leader Lock Failover:** Kill the leader ESS pod. Verify that another pod acquires the Redis leader lock key within 30 seconds and takes over the draining loop.
6. **Verify Draining on Unpause:** Toggle `paused: false`. Verify that buffered tasks are rate-limited, published to Redis, and deleted from Spanner.
