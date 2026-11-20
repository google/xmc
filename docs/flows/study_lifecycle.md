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
> enabling requires `acknowledgeExperimental: true`). Today `xmc-study-controller` validates
> reconcile ticks, serializes per-study execution under an advisory Redis lease lock, and records
> health and Prometheus metrics; reconciling studies against ESS, Vizier, and ClickHouse is still a
> no-op stub.

---

## 1. Runtime Topology

The diagram below shows every live runtime edge involved in actuating `hpo`, submitting a `STUDY`
experiment (including post-commit reconcile enqueueing and study chart provisioning), consuming
study reconcile ticks, and reading the study's parallel-coordinates chart. Solid arrows are
synchronous requests or in-transaction writes; dashed arrows are asynchronous probes, post-commit
side effects, or Redis stream operations.

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
        DB[("Spanner / Postgres<br/>Experiments + Artifacts + Charts")]
    end

    subgraph Bus["Redis"]
        Stream[("xmc-study-reconcile-stream<br/>(study-controller-group)")]
        Lock[("xmc:study-reconcile-lock:{xid}<br/>(70s lease, Lua DEL)")]
        DLQ[("xmc-study-reconcile-dlq-stream")]
    end

    subgraph HPO["HPO Workload (xmc-cmp-hpo)"]
        SC["xmc-study-controller<br/>(:8080 /healthz, /readyz, /metrics)"]
    end

    DS["Dashboard Service"]

    Prof -->|"Component CRs:<br/>hpo, vizier, clickhouse"| Bridge
    Bridge -.->|"TCP :7077 readiness probe"| Viz
    Bridge -.->|"Readiness check"| CH
    Bridge -.->|"Helm install when<br/>vizier & clickhouse Ready"| SC

    Client -->|"CreateExperiment(STUDY)"| ESS
    ESS -->|"CreateXidEntry"| XID
    ESS -->|"1 txn: evaluate deps, clamp budget,<br/>insert STUDY, pin snapshots"| DB
    ESS -.->|"XADD StudyReconcilePayload<br/>(best-effort, 2s timeout)"| Stream
    ESS -.->|"Async CreateChart<br/>(study-{xid}-parallel-coordinates)"| DS

    Stream -.->|"XREADGROUP / XAUTOCLAIM"| SC
    SC <-->|"SET NX PX 70s / Lua DEL<br/>& /readyz PING"| Lock
    SC -.->|"Invalid payload / exhausted retries"| DLQ

    Client -->|"ListCharts / getData<br/>(experiments/{xid}/charts/...)"| DS
    DS -->|"CheckUserAuthorization"| XID
    DS -->|"insert chart / read StudyConfig + WorkUnits"| DB
```

Key boundaries in this topology:

- **Profile gate (`stable-2026b` only)**: `stable-2026a` does not ship `hpo` or `vizier`, so
  `CreateExperiment(STUDY)` fails closed with `FAILED_PRECONDITION` unless `stable-2026b` is
  installed and `hpo`, `vizier`, and `clickhouse` are enabled and `Ready` at their current
  generations.
- **Dependency loss after actuation**: once `xmc-cmp-hpo` is installed, `xmc-bridge` leaves the
  running `xmc-study-controller` workload in place if `vizier` or `clickhouse` later becomes
  unready (holding only future Helm upgrades). `/readyz` on `xmc-study-controller` currently pings
  Redis (`2s` timeout); `/healthz` checks only HTTP liveness so a Redis outage marks the pod
  `NotReady` without restarting it.
- **Unsigned hint stream**: `xmc-study-reconcile-stream` carries an unsigned protojson
  `StudyReconcilePayload` (`{"experiment": "experiments/{xid}"}`,
  [`api/protos/task_queue/task.proto`](../../api/protos/task_queue/task.proto)) with at-least-once
  delivery; see
  [Architecture Overview §7.2](../architecture/overview.md#72-inter-service-task-authentication)
  for the trust-boundary exception.
- **Study chart reads**: the Dashboard Service serves the experiment-level parallel-coordinates
  chart from the experiment database it already opens (the `StudyConfig` and the study's work units)
  after the same XID ACL check as ESS; it never calls `xmc-study-controller` or Vizier (see
  [§3](#3-study-visualization-parallel-coordinates-chart)).

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
    Acq -->|"Acquired"| Run["Run reconcile(ctx, xid) (60s timeout; stub today)<br/>then Lua compare-and-DEL release (2s detached ctx)"]
    Run -->|"Error or SIGTERM cancel"| RetryRun["outcome=retry_error<br/>Warn + re-enqueue (up to 5 retries -> DLQ)"]
    Run -->|"Success"| Ok["outcome=reconciled<br/>Log INFO + XACK"]
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

`GET /metrics` pre-initializes all four `outcome` series on
`xmc_study_controller_reconcile_ticks_total` at startup:

- **`reconciled`**: lock acquired, reconcile completed, lock released, tick acknowledged.
- **`contended`**: another worker already holds `xmc:study-reconcile-lock:{xid}`; the duplicate
  tick is acknowledged immediately without reconciling while the active holder remains responsible
  for the study.
- **`retry_error`**: Redis lock acquisition failed, `reconcile` returned an error, or `SIGTERM`
  cancelled an in-flight tick; `ConsumerClient` re-adds and acknowledges the entry up to
  `RedisMaxRetries` (`5`) before moving it to `xmc-study-reconcile-dlq-stream` (ticks interrupted
  after shutdown begins are always retried rather than dead-lettered unless retries are exhausted).
- **`invalid_payload`**: `data` failed protojson decoding or did not carry a valid
  `experiments/{xid}` resource name; routed straight to `xmc-study-reconcile-dlq-stream` without
  acquiring the lock or retrying.

---

## 3. Study Visualization (Parallel-Coordinates Chart)

When a `STUDY` experiment is created and `ess_dashboard_service_addr` is configured on `ESS` (via
environment variable or the `ess-config` ConfigMap), `ESS`
automatically provisions an experiment-level `PARALLEL_COORDINATES` chart for the study through its
artifact observer pipeline and the dashboard API
([`dashboard_service.proto`](../../api/protos/dashboard/dashboard_service.proto)).

```mermaid
sequenceDiagram
    participant Client as SDK / CLI / UI
    participant ESS as Experiment State Server
    participant XID as XID Service
    participant DB as Spanner / Postgres
    participant DS as Dashboard Service

    Client->>ESS: CreateExperiment(STUDY)
    ESS->>DB: commit STUDY + pin snapshots
    ESS->>XID: CreateXidEntry(parent=xids/{xid}) (post-commit, 10s bound)
    ESS->>DB: insert Artifacts row (parent=experiments/{xid}, ChartArtifact PROVISIONING)
    ESS-->>Client: Experiment
    ESS--)DS: async CreateChart(parent=experiments/{xid}, chart_id=study-{xid}-parallel-coordinates)
    DS->>XID: CheckUserAuthorization(xids/{xid}, WRITER)
    DS->>DB: insert Charts row (Wid NULL)
    DS-->>ESS: Chart (experiments/{xid}/charts/study-{xid}-parallel-coordinates)
    ESS->>DB: UpdateArtifact(status=SUCCESS, chart)
    Client->>DS: GET /v1/experiments/{xid}/charts/study-{xid}-parallel-coordinates:getData
    DS->>XID: CheckUserAuthorization(xids/{xid}, READER)
    DS->>DB: read the experiment's StudyConfig and page through all work units
    DS-->>Client: ParallelCoordinatesData (axes, objective, trajectories)
```

- **Post-commit auto-provisioning**: after the `STUDY` creation transaction commits (next to the
  initial reconcile tick enqueue), `ESS` creates a server-provisioned `ChartArtifact` under
  `experiments/{xid}` (`WorkUnitId` `NULL`, display name and title `"Parallel Coordinates"`,
  `metric_names` listing the study's objective metric ID, initial status `PROVISIONING`, bounded at
  `10s` on a cancellation-detached context that preserves caller auth metadata). User
  `CreateArtifact` calls continue to reject experiment-level `ChartArtifact` payloads with
  `INVALID_ARGUMENT`. The `dashboardObserver` then asynchronously calls `DashboardService.CreateChart`
  with the creating user's forwarded credentials, `parent = experiments/{xid}`, deterministic
  `chart_id = study-{xid}-parallel-coordinates`, and a single `PARALLEL_COORDINATES` plot, writing
  `SUCCESS` and the created `Chart` (or `FAILED` and `error_message`) back to the artifact. When no
  Dashboard Service is configured (`ess_dashboard_service_addr` empty), `ESS` skips provisioning so no
  artifact is left stuck in `PROVISIONING`.
- **Deterministic chart ID & parent binding**: the study chart's resource name is computable from
  the experiment XID as `experiments/{xid}/charts/study-{xid}-parallel-coordinates`. Chart IDs
  matching `^study-\d+-parallel-coordinates$` are reserved for the matching parent `experiments/{xid}`
  (`ValidateCreateChartRequest` rejects any non-matching experiment parent, work unit parent, leading
  zero, or overflowing number with `INVALID_ARGUMENT`), preventing writers of one experiment from
  claiming another study's chart ID.
- **UI rendering status**: on the experiment Artifacts tab, `chart_artifact_view` renders objective
  metric chips and displays status notices while the `ChartArtifact` is `PROVISIONING` or `FAILED`;
  once `SUCCESS`, it renders an empty `"No metric data available"` placeholder card until
  `PARALLEL_COORDINATES` UI rendering support lands.
- **Experiment-level charts**: named `experiments/{xid}/charts/{chart}`, created and listed under
  the parent `experiments/{xid}`, with `Chart.wid` 0 (stored as a `NULL` `Charts.Wid`, migration 021).
  They may only hold `PARALLEL_COORDINATES` plots, and work unit charts may not hold any
  (`INVALID_ARGUMENT`). Access follows the experiment's XID ACL, like work unit charts.
- **Axes**: one axis per `StudyConfig` parameter (double or integer range, categorical values in
  configuration order, discrete values ascending, plus the parameter's scale), followed by the
  objective metric and its goal.
- **Trajectories**: one per work unit in ascending ID order, with the state of its current
  execution, its creation time, and the time it completed (unset while it runs). Trajectories do not
  carry parameter values or the objective value yet.
- **Failure modes & recovery**: provisioning failures never fail `CreateExperiment(STUDY)` — `ESS`
  logs the error and increments `xmc_ess_study_chart_provisioning_failures_total` with
  `stage="artifact"` (child XID allocation or artifact insert failure) or `stage="chart"`
  (`DashboardService.CreateChart` failure or `UpdateArtifact` status write-back failure). Failed or
  missing study charts are not automatically re-provisioned yet (deferred to future sweeper or
  controller recovery). On the read path, `GetChartData` returns `FAILED_PRECONDITION` when the
  experiment is not a `STUDY` with a `StudyConfig`, and, like every chart, when no metrics store
  (ClickHouse or BigQuery) is configured; it also fails while a configured metrics store is
  unreachable, because the chart data engine is built on it. A missing experiment-level chart, or an
  experiment-level name that points at a work unit chart or another experiment's chart, returns
  `NOT_FOUND` (unlike work unit charts, no default chart is synthesized).
- **Other chart RPCs**: `GetChart`, `UpdateChart`, and `DeleteChart` accept only `charts/{chart}`
  names, for experiment-level and work unit charts alike.
