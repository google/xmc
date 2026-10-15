# Use Cases

The architecture from three points of view. Each scenario is end to end: what the person does, what
the system does, and where to read further.

| Persona | Wants | Start at |
| :--- | :--- | :--- |
| [ML researcher](#1-ml-researcher) | Run experiments and understand the results | [§1.1](#11-launch-a-hyperparameter-sweep) |
| [Platform engineer / SRE](#2-platform-engineer--sre) | Deploy XMC and keep it healthy | [§2.1](#21-stand-up-an-xmc-environment) |
| [New contributor](#3-new-contributor) | Change the code without breaking it | [§3.1](#31-orient-yourself) |

---

## 1. ML Researcher

You have a training script and a question. XMC's job is to run the script many times and let you
compare the answers.

### 1.1 Launch a hyperparameter sweep

**What you do.** Define one experiment and one work unit per configuration, then submit.

**What happens.** The client script drives a loop:

1. `CreateExperiment` — allocates the `xid` and records ownership.
2. `UpdateExperimentLaunchState(LAUNCHING)` — declares the loop has started.
3. `CreateWorkUnit` × N — each returns an `Operation` in milliseconds, long before any pod exists.
4. `UpdateExperimentLaunchState(COMPLETED)` — declares the loop finished.

Steps 2 and 4 exist because XMC cannot tell a deliberately small experiment from a submission that
crashed halfway. Declaring the launch boundary is what makes an abandoned submission visible rather
than silently plausible.

Each `CreateWorkUnit` writes state to Spanner and enqueues a signed actuation task; the Actuator
builds a JobSet; Kueue admits it when quota allows. Your 200-work-unit sweep submits in seconds and
then drains through the queue at whatever rate your cluster's capacity permits.

**Where the knobs are.** Priority is the main one — set per experiment and per work unit, combined
into a Kueue `WorkloadPriorityClass`
([Execution Model §6](execution_model.md#6-scheduling-and-priority)). A high-priority experiment can
preempt lower-priority work already running.

**Read more.** [Work Unit Lifecycle §2](../flows/work_unit_lifecycle.md#2-create-work-unit),
[Experiment Lifecycle §1](../flows/experiment_lifecycle.md#1-create-experiment).

### 1.2 Watch it run

**What you do.** Open the experiment in the UI, or poll `GetExperimentStatus`.

**What happens.** Nothing you read touches the cluster. The Reconciler has already observed JobSet
and Kueue state and written it to Spanner; `GetExperimentStatus` returns a rollup — per-state work
unit counts, per-severity message counts — in one round trip.

That indirection is why the UI stays responsive on a thousand-work-unit experiment, and why a
Reconciler restart shows up as *state that stops advancing* rather than an error page.

**Read more.** [Reconciliation](../flows/reconciliation.md).

### 1.3 Track training metrics

**What you do.** Add the `xmc` Python SDK to your training script, call `log_metric`, and set
`XMC_XID` and `XMC_GATEWAY_URL` as environment variables on the work unit — the SDK reads the rest
of its identity from what the Actuator injects.

**What happens.** `log_metric` appends a small tuple to a bounded in-memory buffer and returns — no
network, no serialisation, nothing that can stall a training step. A daemon thread pops batches
every few seconds, harvests identity from the environment, and POSTs to the Ingestion Gateway. From
there: Redpanda → ClickHouse → Dashboard Service → chart.

Two behaviours to know before you wonder where your data went: the SDK fails quietly (a pod missing
mandatory identity logs once, silences itself, and lets the job run), and in a distributed job
**only rank zero emits by default**.

**What you get.** Charts downsampled server-side to your plot's pixel width, preserving min/max
envelopes — a million-step run renders honestly without transferring a million points.

**Read more.** [Metrics & Observability §1](../flows/metrics_and_observability.md#1-the-metrics-pipeline),
[Python SDK README](../../sdks/python/xmc/README.md).

### 1.4 Diagnose a work unit that is not running

Which state it is stuck in tells you which subsystem to look at:

| State | Meaning | Where the explanation comes from |
| :--- | :--- | :--- |
| `SCHEDULING_PAUSED` | Global workload scheduling is paused by an operator. | The UI displays a scheduling-paused banner; the execution status records an `INFO` system message ("Scheduling has been paused by administrators. This work unit will be scheduled once scheduling is resumed."); the task is buffered in Spanner (`PausedTasks`) and will be replayed automatically when scheduling resumes. |
| `SCHEDULING` | Kueue has not admitted it. | Workload Reconciler writes the `Inadmissible` reason as a status message — usually insufficient quota. |
| `STARTING` | Admitted, pods not ready. | JobSet Reconciler's startup pod check reports image pull and scheduling errors after ~2 minutes. |
| `RUNNING`, but wrong | Your code. | Log Monitor surfaces lines your job prefixes with `XMC LOG:`, with severity and stack trace. |

All three land in the same place — the work unit's status messages — so the UI answers "why is this
not working?" without cluster access.

**When the status messages are not enough.** They are a summary, and a summary can be missing the
detail you need. With cluster access, the chain from work unit to pod is:

```bash
# The JobSet for a work unit — xmc stamps its identity onto the objects it creates
kubectl get jobset -A -l xmc.google.com/experiment-id=<xid>,xmc.google.com/work-unit-id=<wid>

# Its pods, and why one is not scheduling or not pulling — pods are found by JobSet name,
# not by xmc labels
kubectl get pod -n <ns> -l jobset.sigs.k8s.io/jobset-name=<jobset>
kubectl describe pod -n <ns> -l jobset.sigs.k8s.io/jobset-name=<jobset>

# What Kueue decided, and whether the queue had room for it
kubectl describe workload -n <ns> <workload>
kubectl describe clusterqueue <queue>
```

Container logs outlive the pod in Cloud Logging, which is where to look once a failed attempt has
been cleaned up. The UI's per-work-unit deep links go there directly — they are built from the
cluster, location, and namespace recorded alongside each execution, and query on the same
`jobset-name` pod label
([`internal/toollibs/execution_environments/gke.go`](../../internal/toollibs/execution_environments/gke.go)).

**Read more.** [Metrics & Observability §3](../flows/metrics_and_observability.md#3-log-monitoring-pipeline),
[Reconciliation §2–3](../flows/reconciliation.md#2-jobset-reconciler).

### 1.5 Retry a failed work unit

**What you do.** Call `RestartWorkUnit`, or click restart in the UI.

**What happens.** XMC does not restart anything. It marks the current `WorkUnitExecution` inactive,
creates a new one with a fresh `eid`, and replaces the JobSet with a new one carrying that `eid`.

This is the whole point of the
[Non-Restarting JobSets Contract](execution_model.md#4-the-non-restarting-jobsets-contract): the
failed attempt is preserved as history with its own logs and metrics, and the only thing that ever
re-runs work is an explicit human decision. `ListWorkUnitExecutions` shows you every attempt.

**Read more.** [Work Unit Lifecycle §4](../flows/work_unit_lifecycle.md#4-restart-work-unit).

### 1.6 Stop work you no longer need

`StopWorkUnit` moves the work unit to `STOPPING` immediately and enqueues a JobSet deletion. The
finalizer guarantees the Reconciler records the terminal state before the JobSet disappears, so
`STOPPED` always arrives — a stop cannot silently vanish.

Metrics and status messages already written survive; only the compute stops.

---

## 2. Platform Engineer / SRE

You run XMC for other people. Your concerns are provisioning, capacity, and knowing when something
is broken.

### 2.1 Stand up an XMC environment

**Layers.** Cluster Toolkit blueprints (`deploy/ctk_v2`) provision the GKE cluster, Spanner
databases, Pub/Sub topics, the Log Monitor Cloud Run service, and networking. A Helm chart
(`deploy/helm`) installs the in-cluster services. Optional addons (`deploy/addons`) bring Redpanda
and ClickHouse for the metrics pipeline.

**What is optional.** More than you might expect, and deliberately:

| Piece | Consequence of omitting it |
| :--- | :--- |
| Redpanda + ClickHouse | No metrics charts; everything else works. |
| Log Monitor module | No parsed application errors; scheduling and startup diagnostics still appear. |
| Dashboard Service | No charts UI. |
| ROD | No capacity views. |

The control plane — ESS, XID, Actuator, Reconciler, Redis, Spanner — is the irreducible core.
Each optional component is gated by a Helm value.

**Read more.** [Deployment Topologies](../design/topologies.md),
[Architecture Overview §8](overview.md#8-deployment-shape).

### 2.2 Expose it safely

A single GKE Gateway API `Gateway` fronts everything user-facing, with `HTTPRoute` and `GRPCRoute`
resources per service and cert-manager issuing certificates through Google Trust Services.

The exception is the Ingestion Gateway: `ClusterIP`, no `HTTPRoute`, guarded by a `NetworkPolicy`
that admits only in-cluster pods. Its clients are training jobs, and a high-throughput unauthenticated
ingest endpoint has no business being on the public internet.

Authentication is OIDC, validated identically in production, CI, and local development — the
**Zero-Bypass Authentication Invariant**. Ephemeral environments run CNCF Dex rather than a mock,
so there is no code path where auth is skipped and therefore no code path that goes untested.

**Read more.** [Gateway & Traffic Routing Design](../design/gateway.md),
[Architecture Overview §5](overview.md#5-request-routing-at-the-edge).

### 2.3 Allocate capacity

Quota lives in Kueue `ClusterQueue` objects, not in XMC. XMC's contribution is mapping its
two-level priority model onto Kueue `WorkloadPriorityClass` objects
([Execution Model §6](execution_model.md#6-scheduling-and-priority)) and labelling every JobSet with
its queue and priority class.

This division is intentional: quota policy is a cluster concern with its own well-supported
controller, and duplicating it inside XMC would mean two sources of truth for the same decision.

`GetRunningAndPendingJobs` and `GetSubmitQueue` from the ROD Data Provider give you the live view,
served from a watch-backed cache so dashboards do not translate into API server load.

**Read more.** [Metrics & Observability §5](../flows/metrics_and_observability.md#5-resource-observability-rod).

### 2.4 Answer "is XMC up?"

`GetHealthStatus` probes ESS, Actuator, and Reconciler over HTTP `/readyz`, XID over gRPC health,
the Log Monitor via Cloud Run, and both Spanner databases.

The Spanner checks also compare live schema versions against what the binaries expect, so a
partially applied migration reports as unhealthy immediately instead of manifesting later as
confusing runtime errors.

Results are cached for 15 seconds and `force_refresh` is rate-limited, degrading to stale data
rather than letting a dashboard stampede every component.

**Read more.** [Metrics & Observability §4](../flows/metrics_and_observability.md#4-platform-health).

### 2.5 Debug a stuck actuation

An operation that never completes means a task did not make it through the queue. Check, in order:

1. **The Redis stream** — is the message pending in `actuator-group` on `xmc-tasks-stream`?
2. **The DLQ** — messages exhaust 5 retries (2-minute processing timeout each) and land in
   `xmc-dlq-stream` with a failure reason.
3. **Abandoned messages** — a crashed Actuator leaves messages unacknowledged; they are reclaimed
   after 5 minutes idle.
4. **Actuator logs** — signature or OIDC verification failures reject the task at the boundary.

**Read more.** [Work Unit Lifecycle §8](../flows/work_unit_lifecycle.md#8-failure-handling-along-the-path).

### 2.6 Pause and resume workload scheduling

When the cluster needs maintenance (for example, node pool upgrades or Kueue reconfiguration), an
operator with the admin role can pause workload scheduling from the `/admin` panel
(`PauseScheduling` on the Health Service) rather than rejecting user submissions. While paused, the
Actuator intercepts `Create`, `Restart`, and `Update` tasks into Spanner (`PausedTasks`) and marks
new or restarted executions `SCHEDULING_PAUSED`, while `StopWorkUnit` (`Delete`) continues to
execute immediately. Resuming (`ResumeScheduling`) triggers the ESS resumption worker to drain
buffered tasks back to `xmc-tasks-stream` in priority order with backlog throttling.

**Read more.** [Work Unit Lifecycle §7](../flows/work_unit_lifecycle.md#7-pause-and-resume-workload-scheduling).

### 2.7 Manage reusable components

The Components subsystem (CMS in the ESS, plus the `xmc-bridge` GitOps synchroniser) maintains a
catalog of reusable AI software modules in Spanner, with dependency DAG validation and cycle
detection, reconciled bidirectionally against `Component.xmc.dev` custom resources.

**Read more.** [Components Subsystem README](../../pkg/components/README.md),
[Component Reference §5](components.md#5-components-subsystem).

---

## 3. New Contributor

### 3.1 Orient yourself

Read in this order:

1. [Architecture Overview](overview.md) — the four planes and the two loops.
2. [Execution Model](execution_model.md) — the resource hierarchy and state machine. Most bugs are
   state machine bugs.
3. The [flow doc](../flows/README.md) for the area you are changing.
4. [Component Reference](components.md) — which binary owns what.

The single most useful idea to internalise: **reads never touch Kubernetes, and writes never wait
for it.** Almost every design decision in the repo follows from that.

### 3.2 Find the code

| Change | Where |
| :--- | :--- |
| Add or change an RPC | `api/protos/` → regenerate → handler in `cmd/<service>/server/` |
| Change how a JobSet is built | `cmd/actuator/k8s_client/` |
| Change how cluster state maps to work unit state | `cmd/reconciler/*/converter.go` |
| Change domain rules or validation | `internal/domain/` |
| Change UI behaviour | `cmd/ui/` |
| Change metrics ingestion | `cmd/ingestion_gateway/`, `sdks/python/xmc/` |

**Read more.** [Developer Documentation](../developer.md) for build, test, and lint commands.

### 3.3 Add an RPC

1. Define it in the relevant `api/protos/**/*.proto`, with `google.api.http` annotations for
   gRPC-Gateway transcoding. Follow AIP conventions: AIP-127 string IDs, AIP-160 filters for
   `Search*`, `google.longrunning.Operation` for anything asynchronous.
2. Regenerate stubs (see
   [Protocol Buffers & gRPC Stubs](../developer.md#protocol-buffers--grpc-stubs)).
3. Implement the handler. Authorize through the XID Service, not ad hoc.
4. Decide the shape: synchronous read, synchronous write, or asynchronous actuation
   ([Main Flows](../flows/README.md#anatomy-of-an-xmc-request)). Anything that changes the cluster
   must be the third — return an `Operation` and enqueue a task.
5. Add an `HTTPRoute` or `GRPCRoute` entry if the surface is new.

### 3.4 Understand the invariants before you break them

Two rules in this codebase are enforced in code and will reject your change:

- **Non-Restarting JobSets** — `MaxRestarts` must be `0` and pod `restartPolicy: Always` is
  forbidden on main containers. The Actuator validates this before submission. Pod-level
  `BackoffLimit` is the supported retry mechanism.
  ([Execution Model §4](execution_model.md#4-the-non-restarting-jobsets-contract))
- **Zero-Bypass Authentication** — OIDC validation is identical in production, CI, and local
  development. Do not add an environment check that skips it; run Dex instead.

### 3.5 Run something locally

Not everything runs on a laptop — XMC depends on Spanner, GKE, and Kueue. The practical path is a
developer test cluster with port-forwarding for the pieces you are working on:

```bash
kubectl port-forward svc/ingestion-gateway 8080:8080
```

[`deploy/local/README.md`](../../deploy/local/README.md) covers cluster setup, the `xmc-diag`
health check, submitting metric payloads by hand, and verifying them with ClickHouse queries.

### 3.6 Test what you changed

`docs/developer.md` has the authoritative commands. The general shape: Bazel for Go and Python,
component-specific targets for the UI, and pre-commit hooks running `gofmt`, `ruff`, `prettier`,
`eslint`, `buildifier`, and `addlicense`.

### 3.7 Work with the repo's agent skills

Much of the above is encoded for coding agents under [`.agents/`](../../.agents), and it is worth
knowing about even if you never invoke an agent — it is the most precise statement of how this repo
expects work to be done.

[`.agents/AGENTS.md`](../../.agents/AGENTS.md) is the entry point. It carries two routing matrices:
one maps a touched file's extension to a mandatory language guideline under
[`.agents/rules/languages/`](../../.agents/rules/languages), the other maps a task intent to the
skill and rule file that govern it. Everything else hangs off those tables.

[`.agents/skills/`](../../.agents/skills) holds the playbooks themselves. The ones a contributor
meets first:

| Skill | Covers |
| :--- | :--- |
| `xmc_feature_development` | Requirements scoping, affected-test matrices, coverage, pre-commit verification. |
| `xmc_create_pr` | Branch naming, PR body template, the confirmation gates before a PR is opened or un-drafted. |
| `xmc_review_pr` | The review checklist applied to incoming PRs. |
| `xmc_address_pr_comment` | How review comments are resolved and replied to. |
| `xmc_deploy`, `xmc_upgrade`, `xmc_cleanup_environment` | Bringing a development environment up, forward, and down. |

The rules under [`.agents/rules/`](../../.agents/rules) are where the non-negotiables live —
[`architectural_contracts.md`](../../.agents/rules/architectural_contracts.md) restates the two
invariants in §3.4, and [`github_authoring_and_prs.md`](../../.agents/rules/github_authoring_and_prs.md)
covers branch prefixes, Conventional Commits, and PR sizing gates.

---

## 4. Related Reading

- [Architecture Overview](overview.md) — the component landscape.
- [Main Flows](../flows/README.md) — sequence-level detail for every path above.
- [Developer Documentation](../developer.md) — build, test, and lint.
