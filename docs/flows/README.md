# Main Flows

Sequence-level documentation for XMC's principal request, actuation, and observation paths. Read
[Architecture Overview](../architecture/overview.md) for the component landscape and
[Execution Model](../architecture/execution_model.md) for the state machine these flows drive.

| Document | Covers |
| :--- | :--- |
| [Experiment Lifecycle](experiment_lifecycle.md) | Creating, reading, updating, and listing experiments; launch state; status; artifacts; status messages. |
| [Work Unit Lifecycle](work_unit_lifecycle.md) | Creating, stopping, restarting, and updating work units — the asynchronous actuation path end to end. |
| [Reconciliation](reconciliation.md) | How cluster state becomes persisted work unit state: JobSet and ClusterQueue reconcilers, Kueue sync worker, and the batcher. |
| [Metrics & Observability](metrics_and_observability.md) | The metrics pipeline from training job to dashboard, plus logs, health, and resource observability. |

---

## Anatomy of an XMC Request

Almost every flow in this section is a variation on one of three shapes. Recognising which shape
you are looking at answers most questions about latency, consistency, and failure behaviour.

### Shape 1 — Synchronous read

```mermaid
sequenceDiagram
    participant Client
    participant ESS as ESS
    participant XID as XID Service
    participant Spanner

    Client->>ESS: RPC
    ESS->>ESS: Authenticate & validate
    ESS->>XID: CheckAuth(READER)
    XID-->>ESS: Authorized
    ESS->>Spanner: Query
    Spanner-->>ESS: Rows
    ESS-->>Client: Response
```

Fully synchronous, strongly consistent, no cluster involvement. `GetExperiment`, `ListWorkUnits`,
`ListStatusMessages`, and every `Search*` RPC take this shape.

### Shape 2 — Synchronous write

Same as shape 1, but authorized with `WRITER` (or `OWNER` for deletion) and committing a Spanner
transaction. `CreateExperiment`, `UpdateExperiment`, and the artifact mutations take this shape.
The response reflects committed state.

### Shape 3 — Asynchronous actuation

```mermaid
sequenceDiagram
    participant Client
    participant ESS as ESS
    participant XID as XID Service
    participant Spanner
    participant Redis
    participant Actuator
    participant K8s as Kubernetes

    Client->>ESS: Mutating RPC
    ESS->>XID: CheckAuth(WRITER)
    XID-->>ESS: Authorized
    ESS->>Spanner: Persist state change + Operation
    ESS->>Redis: XADD signed actuation payload
    ESS-->>Client: Operation (not yet done)

    Redis-->>Actuator: XREADGROUP (actuator-group)
    Actuator->>Actuator: Verify OIDC token + payload signature
    Actuator->>K8s: Create / delete / restart / re-prioritise JobSet
    K8s-->>Actuator: Result
    Actuator->>Spanner: UpdateOperation(done=true)
```

Any RPC that must change the cluster takes this shape: `CreateWorkUnit`, `StopWorkUnit`,
`RestartWorkUnit`, `UpdateWorkUnit`, and their `Batch*` variants. The client's response arrives
before the cluster has changed; convergence is observed either by polling the returned
`google.longrunning.Operation` or by watching the work unit's state.

---

## Reading the Diagrams

Every diagram on these pages is a fenced ` ```mermaid ` block, which GitHub renders natively. The
block *is* the source — there is no separate diagram file to keep in step, so a flow change is one
edit to one file, and the diagram cannot silently disagree with the prose beside it.

Flows that have both a synchronous client-facing half and an asynchronous Actuator-side half are
drawn as a single diagram rather than split in two, because the seam between the halves is usually
the thing worth seeing.

The three shapes above are the canonical drawings. A flow that is one of them unmodified is not
redrawn on its own page — it links back here, and the page spends its words on what is specific to
that RPC. A diagram appears where the flow genuinely departs from the shape: `CreateExperiment`
allocating an `xid` before the row exists, `ListExperiments` inverting the authorization order, and
every actuation path in [Work Unit Lifecycle](work_unit_lifecycle.md).
