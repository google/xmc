# Study Lifecycle (HPO)

End-to-end architecture and tick-processing flow for `STUDY` experiments orchestrated by the
Hyperparameter Optimization (`hpo`) component. Read
[Experiment Lifecycle](experiment_lifecycle.md#1-create-experiment) for synchronous experiment
creation, [STUDY Experiments](../../cmd/experiment_state_server/README.md#study-experiments) for
validation and component-pinning rules in the Experiment State Server (ESS), and the
[`hpo` Component Blueprint](../../deploy/profiles/stable-2026b/components/hpo/README.md) for Helm
profile values and actuation details.

> [!NOTE]
> `hpo` and `vizier` are `EXPERIMENTAL` components shipped in the preview
> [`stable-2026b`](../../deploy/profiles/stable-2026b/README.md) profile (disabled by default;
> enabling requires `acknowledgeExperimental: true`). Today `xmc-study-controller` resolves the
> `vizier` Component CR endpoint at startup (`503` on `/readyz` until resolved and reachable via
> `Health/Check`), serializes per-study execution under an advisory Redis lease lock,
> authenticates to ESS with its Workload Identity Google ID token to re-read each ticked
> experiment (`STUDY` + `ACTIVE` gate), and idempotently registers active studies in OSS Vizier via
> `VizierService.CreateStudy`; trial suggestion/dispatch and ClickHouse orchestration are not yet
> wired.

---

## 1. Runtime Topology

The diagram below shows every live runtime edge involved in actuating `hpo`, submitting a `STUDY`
experiment, and consuming study reconcile ticks. Solid arrows are synchronous requests or
in-transaction writes; dashed arrows are asynchronous probes or Redis stream operations.

```mermaid
flowchart LR
    subgraph Admin["Profile & Component Plane"]
        Prof["xmc-profile<br/>(stable-2026b)"]
        Bridge["xmc-bridge"]
        Viz[("Vizier :7077<br/>(BYO 3P Component)")]
        CH[("ClickHouse<br/>(3P Component)")]
    end

    subgraph Control["Control Plane"]
        Client["SDK / CLI / gRPC Client"]
        ESS["Experiment State Server"]
        XID["XID Service"]
        DB[("Spanner / Postgres<br/>Experiments + Component Snapshots")]
    end

    subgraph Bus["Redis"]
        Stream[("xmc-study-reconcile-stream<br/>(study-controller-group)")]
        Lock[("xmc:study-reconcile-lock:{xid}<br/>(70s lease, Lua DEL)")]
        DLQ[("xmc-study-reconcile-dlq-stream")]
    end

    subgraph HPO["HPO Workload (xmc-cmp-hpo)"]
        SC["xmc-study-controller<br/>(:8080 /healthz, /readyz, /metrics)"]
    end

    Prof -->|"Component CRs:<br/>hpo, vizier, clickhouse"| Bridge
    Bridge -.->|"Readiness probe :7077<br/>(TCP, or gRPC health opt-in)"| Viz
    Bridge -.->|"Readiness check"| CH
    Bridge -.->|"Helm install when<br/>vizier & clickhouse Ready"| SC
    Prof -.->|"Startup: read vizier CR<br/>spec.parameters.endpoint"| SC

    Client -->|"CreateExperiment(STUDY)"| ESS
    ESS -->|"CreateXidEntry"| XID
    ESS -->|"1 txn: evaluate deps, clamp budget,<br/>insert STUDY, pin snapshots"| DB
    ESS -.->|"XADD StudyReconcilePayload<br/>(best-effort, 2s timeout)"| Stream

    Stream -.->|"XREADGROUP / XAUTOCLAIM"| SC
    SC <-->|"SET NX PX 70s / Lua DEL<br/>& /readyz PING"| Lock
    SC -->|"GetExperiment(experiments/{xid})<br/>(WI GSA Google ID token)"| ESS
    SC -->|"CreateStudy(owners/xmc, study-{xid})<br/>& /readyz Health/Check"| Viz
    SC -.->|"Invalid payload / invalid study /<br/>exhausted retries"| DLQ
```

Key boundaries in this topology:

- **Profile gate (`stable-2026b` only)**: `stable-2026a` does not ship `hpo` or `vizier`, so
  `CreateExperiment(STUDY)` fails closed with `FAILED_PRECONDITION` unless `stable-2026b` is
  installed and `hpo`, `vizier`, and `clickhouse` are enabled and `Ready` at their current
  generations.
- **Dependency loss after actuation**: once `xmc-cmp-hpo` is installed, `xmc-bridge` leaves the
  running `xmc-study-controller` workload in place if `vizier` or `clickhouse` later becomes
  unready (holding only future Helm upgrades). `/readyz` on `xmc-study-controller` checks both
  Redis (`Ping`) and Vizier (`grpc.health.v1.Health/Check`) within a `2s` timeout; `/healthz`
  checks only HTTP liveness so a dependency outage marks the pod `NotReady` without restarting it.
- **Unsigned hint stream**: `xmc-study-reconcile-stream` carries an unsigned protojson
  `StudyReconcilePayload` (`{"experiment": "experiments/{xid}"}`,
  [`api/protos/task_queue/task.proto`](../../api/protos/task_queue/task.proto)) with at-least-once
  delivery; see [Architecture Overview §7.2](../architecture/overview.md#72-signed-task-payloads)
  for the trust-boundary exception.

---

## 2. Per-Tick Processing & Per-Study Lease Lock

`xmc-study-controller` ([`cmd/study_controller`](../../cmd/study_controller)) shares a single
`*redis.Client` connection pool across stream consumption (`ConsumerClient`), per-study lease
locking ([`internal/redislock`](../../internal/redislock)), and `/readyz`. Each delivered stream
entry follows the decision path below:

```mermaid
flowchart TD
    Msg["XREADGROUP / XAUTOCLAIM entry<br/>(study-controller-group)"] --> HasData{"Entry has string<br/>'data' field?"}
    HasData -->|"No (stream consumer)"| MissingDLQ["Write to xmc-study-reconcile-dlq-stream<br/>(reason=missing_data) + XACK"]
    HasData -->|"Yes"| Parse{"Valid protojson<br/>experiments/{xid}?"}
    Parse -->|"No"| Inv["outcome=invalid_payload<br/>Warn + move to DLQ + XACK (no retry)"]
    Parse -->|"Yes"| Acq["SET xmc:study-reconcile-lock:{xid} token NX PX 70s<br/>(16-byte crypto/rand hex token)"]
    Acq -->|"NX miss (ErrNotAcquired)"| Cont["outcome=contended<br/>Log INFO + XACK (skip reconcile)"]
    Acq -->|"Redis error"| RetryAcq["outcome=retry_error<br/>Warn + re-enqueue (up to 5 retries -> DLQ)"]
    Acq -->|"Acquired"| Read["ESS GetExperiment(experiments/{xid})<br/>(60s timeout, WI GSA ID token)"]
    Read -->|"NotFound / non-STUDY /<br/>active_state != ACTIVE"| Skip["Log INFO skip<br/>release lease (2s detached)<br/>outcome=reconciled + XACK"]
    Read -->|"Other ESS error or SIGTERM"| RetryRun["outcome=retry_error<br/>release lease + re-enqueue (up to 5 retries -> DLQ)"]
    Read -->|"STUDY & ACTIVE"| VizCreate["VizierService.CreateStudy<br/>(owners/xmc, display_name=study-{xid})"]
    VizCreate -->|"OK (created or existing)"| Ok["Log registered study<br/>release lease (2s detached)<br/>outcome=reconciled + XACK"]
    VizCreate -->|"ErrInvalidStudyConfig (not cancelled)"| InvStudy["outcome=invalid_study<br/>release lease + move to DLQ + XACK (no retry)"]
    VizCreate -->|"Transient gRPC error or SIGTERM"| RetryRun
```

### Lease, Timeout, and Shutdown Invariants

| Parameter | Value | Role |
| :--- | :--- | :--- |
| Lock key | `xmc:study-reconcile-lock:{xid}` | Isolates locking per study (`int64` XID) so distinct studies reconcile concurrently across workers. |
| Lease TTL | `70s` | Exceeds the `60s` reconcile timeout (`< 2m` `RedisProcessTimeout`) so an active reconcile never outlives its lease without needing a background renewal goroutine. |
| Reconcile timeout | `60s` | Bounds per-tick execution below `ConsumerClient`'s `2m` process timeout so the handler cancels work and releases the lock before stream timeout handling fires. |
| Release timeout | `2s` (detached) | Runs `if GET == token then DEL` on `context.WithoutCancel(ctx)` so `SIGTERM` shutdown still frees the lock in Redis rather than leaving a `70s` orphan. |
| Consumer drain | `10s` (`5s` HTTP first) | Covers the `2s` `XREADGROUP` block or `2s` detached lock release plus `5s` stream settle timeout (`SettleTimeout`) before closing the shared Redis client. |
| Abandoned PEL claim | `5m` (`XAUTOCLAIM`) | Reclaims unacknowledged stream entries if a controller pod crashes (`SIGKILL` / OOM) mid-reconcile after its `70s` Redis lease has expired. |

### Outcomes & Observability

`GET /metrics` pre-initializes all five `outcome` series on
`xmc_study_controller_reconcile_ticks_total` at startup:

- **`reconciled`**: lock acquired, reconcile completed (including idempotent Vizier study
  registration for active `STUDY` experiments), lock released, tick acknowledged.
- **`contended`**: another worker already holds `xmc:study-reconcile-lock:{xid}`; the duplicate
  tick is acknowledged immediately without reconciling while the active holder remains responsible
  for the study.
- **`retry_error`**: Redis lock acquisition failed, `reconcile` returned a transient error, or
  `SIGTERM` cancelled an in-flight tick; `ConsumerClient` re-adds and acknowledges the entry up to
  `RedisMaxRetries` (`5`) before moving it to `xmc-study-reconcile-dlq-stream` (ticks interrupted
  after shutdown begins are always retried rather than dead-lettered unless retries are exhausted).
- **`invalid_payload`**: `data` failed protojson decoding or did not carry a valid
  `experiments/{xid}` resource name; routed straight to `xmc-study-reconcile-dlq-stream` without
  acquiring the lock or retrying.
- **`invalid_study`**: `StudyConfig` failed forward conversion or Vizier returned
  `InvalidArgument` (while `ctx` is not cancelled); routed straight to
  `xmc-study-reconcile-dlq-stream` without retrying or logging user payload contents.
