# Component Reference

Every deployable binary and shared library in XMC, what it is responsible for, and what it talks
to. Read [Architecture Overview](overview.md) first for how these fit together.

---

## 1. Deployment Inventory

| Component | Source | Runs on | Protocol & port | Helm values gate |
| :--- | :--- | :--- | :--- | :--- |
| Experiment State Server (ESS) | [`cmd/experiment_state_server`](../../cmd/experiment_state_server) | GKE `Deployment` (HPA) | gRPC `50051`, HTTP `8080` | `essDeployment.enabled` (default `true`) |
| XID Service | [`cmd/xid_service`](../../cmd/xid_service) | GKE `Deployment` (HPA) | gRPC `50051` | always |
| Actuator | [`cmd/actuator`](../../cmd/actuator) | GKE `Deployment` (VPA) | HTTP `8080` (health & metrics) | always |
| Reconciler | [`cmd/reconciler`](../../cmd/reconciler) | GKE `Deployment` (VPA) | HTTP `8080` (health & metrics) | always |
| Web UI | [`cmd/ui`](../../cmd/ui) | GKE `Deployment` (Nginx) | HTTP `80` | `ui.enabled` (default `true`) |
| Health Service | [`cmd/health_service`](../../cmd/health_service) | GKE `Deployment` | HTTP `8080`, gRPC | `healthServiceDeployment.enabled` |
| Dashboard Service | [`cmd/dashboard_service`](../../cmd/dashboard_service) | GKE `Deployment` | HTTP `8080`, gRPC | `dashboardServiceDeployment.enabled` |
| ROD Data Provider | [`cmd/rod_data_provider`](../../cmd/rod_data_provider) | GKE `Deployment` | HTTP `8080`, gRPC | `rodDeployment.enabled` |
| Ingestion Gateway | [`cmd/ingestion_gateway`](../../cmd/ingestion_gateway) | GKE `Deployment` (HPA) | HTTP `8080` | `ingestionGatewayDeployment.enabled` |
| XMC Bridge | [`cmd/xmc_bridge`](../../cmd/xmc_bridge) | GKE `Deployment` | gRPC client only | `componentsDeployment.enabled` |
| Log Monitor | [`cmd/log_monitor`](../../cmd/log_monitor) | Cloud Run service | HTTP health endpoint | Terraform module |
| Component Linter | [`cmd/component-linter`](../../cmd/component-linter) | Developer workstation / CI | CLI | n/a |

Container images are pinned per service through the `*_custom_image` values in
[`deploy/helm/values.yaml`](../../deploy/helm/values.yaml).

---

## 2. Control Plane

### 2.1 Experiment State Server (ESS)

**Source:** [`cmd/experiment_state_server`](../../cmd/experiment_state_server) ·
**API:** [`api/protos/experiment/api.proto`](../../api/protos/experiment/api.proto)

ESS is the front door for everything about experiment state. It serves `ExperimentStateService`
over gRPC on `:50051` and the same surface over HTTP on `:8080` via gRPC-Gateway transcoding, so
the CLI and the UI exercise identical server code.

Its RPC surface groups into five families:

| Family | RPCs |
| :--- | :--- |
| Experiments | `CreateExperiment`, `GetExperiment`, `UpdateExperiment`, `ListExperiments`, `SearchExperiments`, `GetExperimentStatus`, `UpdateExperimentLaunchState` |
| Work units | `CreateWorkUnit`, `GetWorkUnit`, `UpdateWorkUnit`, `ListWorkUnits`, `SearchWorkUnits`, `StopWorkUnit`, `BatchStopWorkUnits`, `RestartWorkUnit`, `BatchRestartWorkUnits` |
| Executions | `GetCurrentWorkUnitExecution`, `ListWorkUnitExecutions` |
| Artifacts | `CreateArtifact`, `GetArtifact`, `UpdateArtifact`, `DeleteArtifact`, `ListArtifacts`, `SearchArtifacts` |
| Status messages | `ListStatusMessages`, `SearchStatusMessages` |

Each RPC lives in its own file under
[`cmd/experiment_state_server/server`](../../cmd/experiment_state_server/server) — `createworkunit.go`,
`stopworkunit.go`, and so on — which makes the mapping from API to implementation direct.

Responsibilities beyond CRUD:

- **Authorization gate.** Every handler calls `XidService.CheckUserAuthorization` with the role the
  operation requires before reading or writing state.
- **Actuation dispatch.** Mutations that must reach the cluster are persisted as a
  `google.longrunning.Operation` and enqueued to the Actuator's Redis stream; the RPC returns the
  operation rather than blocking.
- **CMS host.** The Component Manager Service is registered on the same gRPC server (see
  [§5.1](#51-component-manager-service-cms)).
- **Resumption worker.** When `XMC_PAUSE_SCHEDULING_ENABLED` is enabled, ESS runs a consumer in the
  `ess-group` consumer group on `xmc-scheduling-resume-stream`
  ([`cmd/experiment_state_server/resumption`](../../cmd/experiment_state_server/resumption)) that
  claims resumption triggers published by the Health Service and replays buffered `PausedTasks`
  from Spanner back to `xmc-tasks-stream` in priority order with backlog throttling.
- **OAuth2 endpoints.** The application-level OAuth2 login, callback, and session handling live in
  [`pkg/auth/oauth2`](../../pkg/auth/oauth2) and are mounted by ESS.

**Depends on:** Cloud Spanner (ESS database), XID Service, Redis.

### 2.2 XID Service

**Source:** [`cmd/xid_service`](../../cmd/xid_service) ·
**API:** [`api/protos/xid/api.proto`](../../api/protos/xid/api.proto)

A deliberately small service with two jobs: allocating globally unique, monotonically increasing
experiment IDs, and answering authorization questions about them. It exposes `XidService` over
gRPC on `:50051` with `GetXidEntry`, `CreateXidEntry`, `UpdateXidEntry`, `ListXidEntries`,
`SearchXidEntries`, and `CheckUserAuthorization`.

It is on the critical path of every ESS mutation, which is why it runs behind its own HPA and
keeps its own Spanner database separate from the ESS database.

**Depends on:** Cloud Spanner (XID database).

### 2.3 Health Service

**Source:** [`cmd/health_service`](../../cmd/health_service) ·
**API:** [`api/protos/health/api.proto`](../../api/protos/health/api.proto)

The operator-facing diagnostic and scheduling control surface, reachable at `/v1/health`:

- `GetHealthStatus` — runs checks in parallel against the ESS and XID Spanner databases (including
  reporting the applied schema migration version), Redis, peer gRPC services via the standard gRPC
  health protocol, peer HTTP services, and Cloud Run services such as the Log Monitor.
- `ListConfigEntries` — returns the effective runtime configuration of the deployment, so operators
  can confirm what a running pod actually has rather than what the chart intended.
- `GetUserInfo` — returns the calling identity as the platform sees it, including group membership
  resolved through Cloud Identity. Administrative responses are gated by an admin role guard.
- `GetSchedulingStatus` — returns whether global workload scheduling is currently paused so the UI
  can surface a status banner to all authenticated users.
- `PauseScheduling` / `ResumeScheduling` — administrative RPCs that toggle the global workload
  scheduling pause state in Spanner (`PauseSchedulingState`), broadcast state transitions to
  Actuator replicas over `xmc-pause-stream`, and (on resume) publish a resumption trigger to
  `xmc-scheduling-resume-stream` for the ESS resumption worker.

**Depends on:** Cloud Spanner (both databases), Redis, ESS, XID, Cloud Run API, Cloud Identity API.

### 2.4 Dashboard Service

**Source:** [`cmd/dashboard_service`](../../cmd/dashboard_service) ·
**API:** [`api/protos/dashboard/dashboard_service.proto`](../../api/protos/dashboard/dashboard_service.proto)

Backs the metrics visualisation experience. Dashboard and chart *definitions* are CRUD resources in
Spanner (`CreateDashboard`, `ListCharts`, `UpdateChart`, …); chart *data* is produced on demand by
`GetChartData`, which compiles the chart definition into a ClickHouse or BigQuery query depending on
the active `METRICS_STORE` (`clickhouse` or `bigquery`).

The query compiler is [`internal/dashboard/dataengine`](../../internal/dashboard/dataengine): it
turns predicates, series definitions, and summary boxes into SQL executed through
[`pkg/clickhouse`](../../pkg/clickhouse) or [`pkg/bigquery`](../../pkg/bigquery). Keeping the engine
separate from the RPC handlers means chart semantics are unit-testable without a server.

**Depends on:** Cloud Spanner, ClickHouse or BigQuery, Cloud Identity API.

### 2.5 ROD Data Provider

**Source:** [`cmd/rod_data_provider`](../../cmd/rod_data_provider) ·
**API:** [`api/protos/rod/api.proto`](../../api/protos/rod/api.proto)

Backend for the Resource Observability Dashboard — the `/resources` view in the UI. It answers
`ListClusterQueues`, `GetRunningAndPendingJobs`, `GetSubmitQueue`, and
`BatchGetQueueSecondaryData`.

Rather than querying the Kubernetes API on every request, it runs its own controller-runtime
reconcilers over Kueue `ClusterQueue`, `LocalQueue`, and `ResourceFlavor` resources and maintains
warm caches ([`cmd/rod_data_provider/cache`](../../cmd/rod_data_provider/cache)) of queue
inventory and accumulated resource usage. Secondary data — the experiment and work unit context
behind a queued job — is joined in from Spanner.

**Depends on:** Kubernetes API (Kueue resources), Cloud Spanner.

### 2.6 Web UI

**Source:** [`cmd/ui`](../../cmd/ui)

An Angular single-page application built and served by Nginx. Routes
([`cmd/ui/app/app_routes.ts`](../../cmd/ui/app/app_routes.ts)):

| Route | View |
| :--- | :--- |
| `/` | Homepage |
| `/experiments` | Experiment list |
| `/experiments/:xid` | Experiment detail with its work units |
| `/experiments/:xid/workUnits/:wid` | Work unit detail, executions, status messages, charts |
| `/resources` | Resource Observability Dashboard (ROD) |
| `/dashboards`, `/dashboards/:id` | Metrics dashboards |
| `/admin` | Admin panel (role-guarded) |

The SPA calls the same `/v1` HTTP surface the CLI uses, so there is no UI-specific backend.

---

## 3. Execution Plane

### 3.1 Actuator

**Source:** [`cmd/actuator`](../../cmd/actuator)

The only component that creates or mutates workload resources in the cluster. It is a Redis stream
consumer, not an HTTP service — its `:8080` port serves health and Prometheus metrics only.

Operation:

1. Reads from the `xmc-tasks-stream` Redis stream as a member of the `actuator-group` consumer
   group.
2. Validates the message's OIDC token and its Tink-signed payload hash
   ([`cmd/actuator/workunittaskhandler`](../../cmd/actuator/workunittaskhandler)).
3. If global workload scheduling is paused (`XMC_PAUSE_SCHEDULING_ENABLED`) and the action is
   `Create`, `Restart`, or `Update`, intercepts the task, buffers it in the Spanner `PausedTasks`
   table, and transitions `Create` and `Restart` executions to `SCHEDULING_PAUSED`
   ([`cmd/actuator/pausescheduling`](../../cmd/actuator/pausescheduling)). `Delete` actions bypass
   the pause so users can always stop work. Each replica keeps an in-memory pause state cache seeded
   from Spanner on startup and updated via `xmc-pause-stream` broadcasts (or fast-forwarded by a
   newer `resumption_time` on replayed task metadata).
4. Otherwise dispatches on the payload's action — `Create`, `Delete`, `Restart`, or `Update` — into
   [`cmd/actuator/k8s_client`](../../cmd/actuator/k8s_client), which builds the `JobSet`
   (labels, annotations, environment variables, Kueue queue and priority-class labels, init
   containers, TPU/GPU node selectors, managed-`xprof` GCS wiring).
5. Marks the corresponding Spanner `Operation` done (for tasks buffered in step 3, the `Operation` remains pending until replay).

Delivery semantics are configured in
[`internal/kubernetes/common/constants`](../../internal/kubernetes/common/constants/constants.go):
5 retries, a 2-minute per-message processing timeout, 5-minute abandoned-message reclamation, and a
`xmc-dlq-stream` dead-letter stream. Payloads carry an idempotency key so redelivery does not
duplicate work.

The Actuator enforces the [non-restarting JobSets contract](execution_model.md#4-the-non-restarting-jobsets-contract)
at construction time — it refuses to submit a JobSet whose `FailurePolicy.MaxRestarts` is not `0`.

**Depends on:** Redis, Kubernetes API (JobSet, Kueue), Cloud Spanner.

### 3.2 Reconciler

**Source:** [`cmd/reconciler`](../../cmd/reconciler)

A controller-runtime manager hosting several controllers and background workers that together turn
cluster reality into persisted work unit execution state.

| Sub-component | Source | Role |
| :--- | :--- | :--- |
| JobSet reconciler | [`jobset`](../../cmd/reconciler/jobset) | Watches `JobSet` resources; syncs status, performs startup-pod error detection, converts JobSet status into a `WorkUnitExecutionUpdate`, and manages the XMC finalizer on deletion. |
| Workload reconciler | [`workload`](../../cmd/reconciler/workload) | Watches Kueue `Workload` resources for admission, preemption, and inadmissibility signals. |
| ClusterQueue reconciler | [`clusterqueue`](../../cmd/reconciler/clusterqueue) | Maintains an in-memory cache of `ClusterQueue` names for the Kueue sync worker. |
| Kueue sync worker | [`kueue_sync`](../../cmd/reconciler/kueue_sync) | Periodically polls Kueue's pending-workloads endpoint per cluster queue to surface queue position for jobs that have not been admitted. |
| Batcher | [`shared`](../../cmd/reconciler/shared) | Coalesces updates from all sources, keyed `xid → wid → source`, and flushes them to Spanner on a tick. |

The batcher exists because a single scheduling event can produce updates from three sources at
once; writing each one straight through would multiply Spanner write load and cause lost-update
races between sources. See [Reconciliation Flows](../flows/reconciliation.md).

**Depends on:** Kubernetes API (JobSet, Kueue `Workload` and `ClusterQueue`, Pods), Cloud Spanner.

---

## 4. Telemetry Plane

### 4.1 Ingestion Gateway

**Source:** [`cmd/ingestion_gateway`](../../cmd/ingestion_gateway) ·
**Detail:** [component README](../../cmd/ingestion_gateway/README.md)

Accepts metric batches at `POST /api/v1/metrics` as a JSON array or NDJSON, tokenises and validates
them in constant memory, and produces protobuf records to the `xmc-metrics` topic. Clients get
`202 Accepted` as soon as the record is buffered.

The message bus behind it is selectable with `-message_bus`:

| Backend | Producer | Notes |
| :--- | :--- | :--- |
| `redpanda` (default) | franz-go async Kafka producer | Asserts partition and replica topology on startup; optional SASL and TLS. |
| `pubsub` | Cloud Pub/Sub publisher | Requires `-project_id`. SASL and TLS are rejected, being Kafka wire-protocol concepts. |

Records are keyed `xid:work_unit_id` on both backends, which is what guarantees FIFO ordering
within a work unit while letting different work units proceed in parallel — Redpanda partitions by
the key, Pub/Sub orders by it.

It is a `ClusterIP` service fronted by a `NetworkPolicy` that only admits in-cluster pods — it is
not published through the GKE Gateway. Training jobs reach it directly by service DNS; developers
reach it with `kubectl port-forward` (see [`deploy/local`](../../deploy/local)).

**Depends on:** Redpanda/Kafka or Cloud Pub/Sub, depending on `-message_bus`.

### 4.2 Log Monitor

**Source:** [`cmd/log_monitor`](../../cmd/log_monitor) ·
**Infrastructure:** [`deploy/ctk_v2/modules/log_monitor`](../../deploy/ctk_v2/modules/log_monitor)

Turns interesting workload log lines into structured status messages that appear alongside a work
unit in the UI. A Cloud Logging sink selects container logs that carry XMC's managed-by and
`xid`/`wid`/`eid` labels *and* match an error signature (a Python traceback or a configurable
keyword), and publishes them to a Pub/Sub topic. The Log Monitor pulls that subscription, parses
each entry into a `StructuredMessage` with a mapped severity and extracted stack trace, groups the
batch by experiment, and writes the updates to Spanner.

Because the filter is applied at the sink, the Log Monitor never sees the full log firehose.

**Depends on:** Pub/Sub, Cloud Spanner.

---

## 5. Components Subsystem

Full documentation: [`pkg/components/README.md`](../../pkg/components/README.md).

### 5.1 Component Manager Service (CMS)

**Source:** [`pkg/components/cms`](../../pkg/components/cms) ·
**API:** [`api/protos/components/v1alpha1/components.proto`](../../api/protos/components/v1alpha1/components.proto)

Hosted inside the ESS gRPC server. Manages the catalog of reusable platform components — their
versions, dependency graph, and rollout state. RPCs cover lifecycle intent
(`SetComponentIntent`, `PrepareDeactivation`, `CompleteDeactivation`, `ExecuteDeletion`,
`UpdateObservedGeneration`) and resolution/validation (`ResolveComponentSnapshot`,
`ValidateComponentRequirement`, `GetPlatformHealth`).

[`pkg/components/store`](../../pkg/components/store) provides the in-memory graph with prospective
DAG cycle detection, so an invalid dependency edge is rejected before it is persisted.

### 5.2 XMC Bridge

**Source:** [`cmd/xmc_bridge`](../../cmd/xmc_bridge) ·
**Detail:** [component README](../../cmd/xmc_bridge/README.md)

A controller daemon that watches `Component.xmc.dev/v1alpha1` custom resources and reconciles them
against CMS over gRPC. It is the seam between GitOps-managed cluster manifests and the
Spanner-backed catalog: cluster intent flows in, observed generation flows back.

### 5.3 Component Linter

**Source:** [`cmd/component-linter`](../../cmd/component-linter) ·
**Detail:** [component README](../../cmd/component-linter/README.md)

An offline CLI that statically validates component manifest directories against a rules
configuration and emits a text or JSON report. It runs in pull request CI so malformed component
declarations fail the build rather than a deployment.

---

## 6. Shared Libraries

| Package | Responsibility |
| :--- | :--- |
| [`internal/storage`](../../internal/storage) | Spanner data access, schema migrations, and the storage interfaces used by ESS, Actuator, Reconciler, Health Service, and Log Monitor. |
| [`internal/task_queue`](../../internal/task_queue) | Builds, signs, and enqueues actuator payloads. Owns the idempotency key and JWT/OIDC signing scheme. |
| [`internal/task_queue_redis`](../../internal/task_queue_redis) | Redis Streams publisher and consumer with consumer groups, retries, abandoned-message reclamation, and dead-lettering. |
| [`internal/broadcast`](../../internal/broadcast) | Redis Streams broadcast publisher and consumer used to fan out scheduling pause and resume events (`xmc-pause-stream`) to Actuator replicas. |
| [`internal/featureflags`](../../internal/featureflags) | Unified feature flag definitions (`XMC_PAUSE_SCHEDULING_ENABLED`) and environment-variable loading across services. |
| [`internal/auth`](../../internal/auth) | Authentication context extraction and authorization decisions shared across services. |
| [`pkg/auth/oauth2`](../../pkg/auth/oauth2) | OAuth2/OIDC provider integration, session lifecycle, middleware, and the gateway auth pipeline. |
| [`internal/domain`](../../internal/domain) | Domain rules for experiment creation and work unit resource derivation, kept free of transport and storage concerns. |
| [`internal/kubernetes`](../../internal/kubernetes) | Shared Kubernetes constants — labels, annotations, finalizers, environment variable names, Redis stream names, resource types. |
| [`internal/query_generator`](../../internal/query_generator) | AIP-160 filter expressions compiled into backend queries. |
| [`internal/validator`](../../internal/validator) | Request validation shared across API handlers. |
| [`internal/security`](../../internal/security) | TLS material handling for in-cluster gRPC. |
| [`internal/logging`](../../internal/logging) | Structured GCP logging with `xid`/`wid`/`eid` context propagation. |
| [`internal/prototest`](../../internal/prototest) | Protobuf test helpers, including the `prototest.Modify` baseline-fixture mutator used across unit tests. |
| [`pkg/clickhouse`](../../pkg/clickhouse) | ClickHouse client used by the Dashboard Service. |
| [`pkg/apis`](../../pkg/apis) | Kubernetes API types for XMC custom resources. |

---

## 7. Client Libraries and CLIs

| Client | Source | Purpose |
| :--- | :--- | :--- |
| XManager CLI | installed from PyPI via `pip install xmanager` and overlaid with local [`api/protos`](../../api/protos) via [`deploy/cli`](../../deploy/cli) | The user-facing CLI from [google-deepmind/xmanager](https://github.com/google-deepmind/xmanager). Launches and manages experiments over gRPC. |
| `xmc` Python SDK | [`sdks/python/xmc`](../../sdks/python/xmc) | Metrics client embedded in training jobs. Separates the training hot path from a background flusher thread so logging never stalls the training loop, and posts batches to the Ingestion Gateway with bounded buffering and exponential backoff. |
| `xdash` Python SDK & CLI | [`sdks/python/xdash`](../../sdks/python/xdash) | Fluent builders and a gRPC client for creating and inspecting dashboards and charts against the Dashboard Service. |

---

## 8. Third-Party Controllers XMC Relies On

| Controller | Role in XMC |
| :--- | :--- |
| [JobSet](https://jobset.sigs.k8s.io/) | Expands a `JobSet` into Jobs and Pods and reports aggregate status. XMC always sets `MaxRestarts: 0`. |
| [Kueue](https://kueue.sigs.k8s.io/) | Gang-admits workloads against `ClusterQueue` quota; provides `WorkloadPriorityClass` ordering and preemption. |
| [cert-manager](https://cert-manager.io/) | Issues TLS certificates through Google Trust Services for the Gateway and in-cluster gRPC. |
| GKE Gateway API controller | Implements the external load balancer for `Gateway`, `HTTPRoute`, and `GRPCRoute`. |
