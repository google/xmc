# HPO 1P Native Component Blueprint (`stable-2026b`)

This Helm sub-chart (`deploy/profiles/stable-2026b/components/hpo/`) defines the XMC 1P Native Component blueprint ([`templates/component.yaml`](templates/component.yaml)) for **Hyperparameter Optimization (HPO)** (`hyperparameter-optimization`) and the `xmc-study-controller` workload that `xmc-bridge` actuates for it ([`templates/deployment.yaml`](templates/deployment.yaml), [`templates/serviceaccount.yaml`](templates/serviceaccount.yaml), [`templates/rbac.yaml`](templates/rbac.yaml)).

> [!NOTE]
> The `xmc-study-controller` workload renders a `Deployment`, `ServiceAccount`, `Role`, and `RoleBinding`. It resolves the live `vizier` `Component` CR endpoint at startup, consumes study reconcile ticks from Redis under a per-study Redis lease lock, authenticates to the Experiment State Server (ESS) with its Workload Identity Google ID token to re-read and gate each ticked experiment (`STUDY` + `ACTIVE`), idempotently registers active studies in OSS Vizier via `CreateStudy`, and reports Redis and Vizier reachability on `/readyz`. It does not yet suggest/dispatch trials or query ClickHouse. The chart renders no `Service`, `NetworkPolicy` or `ConfigMap`, and the `ServiceAccount` carries a Workload Identity annotation when `parameters.studyControllerServiceAccount` is set.

---

## 1. Fixed Blueprint Identity & Contract Fields

The following fields are structural constants of the HPO blueprint defined in [`templates/component.yaml`](templates/component.yaml):

| Manifest Field | Fixed Value | Rationale |
| :--- | :--- | :--- |
| `metadata.name` | `"hpo"` | Canonical catalog identifier managed by the XMC GitOps Bridge Controller (`xmc-bridge`). |
| `metadata.labels["xmc.dev/profile"]` | `"stable-2026b"` | Binds this component definition to the `stable-2026b` release profile. |
| `metadata.labels["xmc.dev/component-type"]` | `"custom-1p-component"` | Identifies the blueprint as an XMC first-party (1P) managed workload. |
| `metadata.labels["app.kubernetes.io/name"]` | `"hpo"` | Standard Kubernetes application label. |
| `spec.componentType` | `COMPONENT_TYPE_CUSTOM_1P_COMPONENT` | Hardcoded as a 1P Native Component actuated via Helm by `xmc-bridge`. |
| `spec.componentClass` | `"hyperparameter-optimization"` | Canonical service class for hyperparameter optimization studies and trial orchestration. |
| `spec.version` / `spec.chartVersion` | `.Chart.Version` (`"0.1.0"`) | Sourced strictly from [`Chart.yaml`](Chart.yaml) as the single source of truth. |
| `spec.stability` | `"EXPERIMENTAL"` | Catalog-declared maturity stage; enabling requires `acknowledgeExperimental: true`. |
| `spec.dependencies` | `["vizier", "clickhouse"]` | Declares upstream dependencies on the [`vizier`](../vizier/README.md) optimization engine and [`clickhouse`](../../../stable-2026a/components/clickhouse/README.md) metrics store. `xmc-bridge` waits for both to be Ready before actuating `hpo`. |

---

## 2. Configuration & Value Mapping ([`values.yaml`](values.yaml))

| Value | Default | Target Spec Field | Description |
| :--- | :--- | :--- | :--- |
| `enabled` | `false` | `spec.enabled` | Enables or disables the `hpo` Component CR in the catalog. |
| `acknowledgeExperimental` | *(omitted)* | `spec.acknowledgeExperimental` | Explicit administrator acknowledgement required when `enabled: true` because `stability` is `EXPERIMENTAL`. |
| `namespace` | `""` (`.Release.Namespace`, or `"xmc"` when `"default"`/`""`) | `Component.metadata.namespace`, `Deployment` `-components_namespace` | Target Kubernetes namespace for the `Component` CR and `xmc-study-controller`'s `-components_namespace` flag (`Role`/`RoleBinding` stay in `.Release.Namespace`). |
| `imageTag` | `""` | `spec.imageTag` | `xmc-study-controller` image tag, used when `global.imageTag` is empty. Omitted from the CR when both are empty. |
| `deletionProtection` | `true` | `spec.deletionProtection` | Prevents accidental deletion of the `Component` CR when enabled. |
| `deletionPolicy` | `"Delete"` | `spec.deletionPolicy` | Resource cleanup behavior on deletion (`"Delete"` or `"Orphan"`). |
| `dependencies` | `["vizier", "clickhouse"]` | `spec.dependencies` | Upstream XMC Component names required before `hpo` is actuated. |
| `parameters.maxTrialBudget` | `"1000"` | `spec.parameters.maxTrialBudget` | Largest `max_trial_budget` a study may use: a positive decimal integer, at most `2147483647`. The Experiment State Server reads it when a study is created, clamps larger budgets to it, and records a WARNING status message on the experiment; a missing or invalid value falls back to `1000`. A change applies to studies created afterwards; existing studies keep their stored budget. `xmc-bridge` also passes the value into the `xmc-cmp-hpo` release values, where it is unused, so changing it triggers a no-op upgrade during which study creation may briefly return `FAILED_PRECONDITION`. The profile release is upgraded with `--reset-then-reuse-values`, so removing an override does not revert it; set `"1000"` explicitly instead. |
| `parameters.studyControllerServiceAccount` | `""` | `spec.parameters.studyControllerServiceAccount` (omitted when empty) | Email of the Google service account that `xmc-study-controller` runs as through Workload Identity, for example `xmc-sc-sa@<project>.iam.gserviceaccount.com` in CTK v2 installs. The Experiment State Server admits this identity on gRPC, allows it to read `STUDY` experiments via `GetExperiment` without XID ACLs, and lets only this identity create the work units (trials) of studies with `BatchCreateWorkUnits`, comparing emails case-insensitively; while it is empty every caller gets `PERMISSION_DENIED` on `BatchCreateWorkUnits` and falls through to normal XID ACLs on `GetExperiment`. `xmc-bridge` passes it to the `xmc-cmp-hpo` release as `hpoDeployment.studyControllerServiceAccount`, which renders it as the `iam.gke.io/gcp-service-account` annotation of the `xmc-study-controller` ServiceAccount. CTK v2 ([`deploy/ctk_v2/xmanager-cloud.yaml`](../../../../ctk_v2/xmanager-cloud.yaml)) provisions the `xmc-sc-sa` Google service account (with `project_roles: []`) and its Workload Identity binding to `xmc-study-controller` automatically (operators pass its email when installing `xmc-profile`); non-CTK installs create the service account and binding manually. |

---

## 3. Actuation Lifecycle

1. **Declare intent**: install the [`stable-2026b` profile](../../README.md) with `components.vizier.enabled=true`, `components.vizier.acknowledgeExperimental=true`, `components.clickhouse.enabled=true`, `components.hpo.enabled=true`, and `components.hpo.acknowledgeExperimental=true`.
2. **Actuate**: once `vizier` and `clickhouse` report `Ready=True`, `xmc-bridge` installs the `xmc-cmp-hpo` release from the embedded chart ([`deploy/profiles/stable-2026b/charts.go`](../../charts.go)). [`templates/component.yaml`](templates/component.yaml) is guarded by `{{- if not (hasPrefix "xmc-cmp-" .Release.Name) -}}` so the `xmc-cmp-hpo` release never re-renders the `Component` CR, and the workload templates are guarded by `{{- if hasPrefix "xmc-cmp-" .Release.Name }}` so the profile release never renders them. While a dependency is not Ready, `hpo` reports `Ready=False` with reason `DependenciesNotReady` and no `xmc-cmp-hpo` release exists.
3. **Deactivate**: setting `components.hpo.enabled=false` makes `xmc-bridge` uninstall `xmc-cmp-hpo`.

---

## 4. `xmc-study-controller` Workload

Rendered only for the `xmc-cmp-hpo` release:

| Object | Name | Notes |
| :--- | :--- | :--- |
| `Deployment` | `xmc-study-controller` | 1 replica of [`cmd/study_controller`](../../../../../cmd/study_controller). Serves `/healthz` (liveness), `/readyz` (readiness, see [Redis Connection](#redis-connection) and [Vizier Connection](#vizier-connection)) and `/metrics` (Prometheus) on HTTP port `8080`. Runs as non-root with a read-only root filesystem, mounts the service account token to read `Component/vizier`, and receives `-components_namespace`. |
| `ServiceAccount` | `xmc-study-controller` | Pod identity. Carries the Workload Identity annotation `iam.gke.io/gcp-service-account` when `parameters.studyControllerServiceAccount` is set. |
| `Role` | `xmc-study-controller-components-reader` | Least-privilege role granting `get` on `components.xmc.dev` restricted to `resourceNames: ["vizier"]`. |
| `RoleBinding` | `xmc-study-controller-components-reader-binding` | Binds `xmc-study-controller` to `xmc-study-controller-components-reader` in the release namespace. |

The pod carries the label `xmc.google.com/vizier-client: "true"`. XMC ships no NetworkPolicy for Vizier, which is bring-your-own; operators who restrict access to their Vizier server can admit pods with this label.

### Securing the Study Controller Identity

The Experiment State Server admits the identity in `parameters.studyControllerServiceAccount` on gRPC, allows it to read any `STUDY` experiment via `GetExperiment` without XID ACLs, and lets only that identity create study trials with `BatchCreateWorkUnits`. Experiment owners and writers can't create trials (see [Trust Boundary](../../../../../cmd/experiment_state_server/README.md#trust-boundary)). Anyone who controls that identity can read studies and create trials, so restrict all three of these:

- **Google service account:** grant `roles/iam.workloadIdentityUser` only to `serviceAccount:<project>.svc.id.goog[<namespace>/xmc-study-controller]`, and grant `roles/iam.serviceAccountTokenCreator` on it to no one.
- **Release namespace:** limit who can create pods or set `serviceAccountName: xmc-study-controller` in the namespace where XMC runs.
- **`hpo` Component:** limit who can edit `Component` resources (`components.xmc.dev`) and the profile values, since they choose the identity.

`xmc-bridge` may only manage the objects named above; their names are allowlisted in the bridge Role ([`deploy/helm/templates/components/rbac.yaml`](../../../../helm/templates/components/rbac.yaml)), so renaming or adding an object requires updating that Role.

### Redis Connection

The Study Controller connects to the XMC Redis server:

- **Address**: the umbrella chart value `redisAddr` reaches `xmc-bridge` as `REDIS_ADDR` in the `xmc-bridge-config` ConfigMap, and `xmc-bridge` passes it to every 1P release as `<component>Deployment.redisAddr`. A `redisAddr` Component parameter is ignored. The chart renders the value as `-redis_addr`. The value is `required`: an `xmc-cmp-hpo` release without `hpoDeployment.redisAddr` fails to render, so the Helm install or upgrade fails instead of starting a Study Controller without Redis. The profile release does not need it.
- **Password**: the `REDIS_PASSWORD` environment variable, read from key `redis-password` of the umbrella chart's `redis-secret` Secret. `xmc-bridge` only actuates Components in the XMC release namespace, where the umbrella chart renders `redis-secret`.
- **Readiness**: `/readyz` checks Redis (`Ping`) and Vizier (`Health/Check`) concurrently within a shared 2s timeout and returns `503 Service Unavailable` when either fails, so the pod reports `NotReady` while Redis or Vizier is unreachable. The readiness probe's `timeoutSeconds` is `3`. `/healthz` does not check dependencies, so an outage never restarts the pod. The `hpo` Component's `Ready` condition does not reflect dependency reachability, because `xmc-bridge` checks workload health at the Helm release level; only the pod's readiness does.
- **Troubleshooting**: if `redis-secret` is missing, the pod stays in `CreateContainerConfigError`; `kubectl describe pod -l app=xmc-study-controller` names the missing Secret. If the pod is `Running` but not `Ready`, its logs show the failing Redis ping or Vizier resolution/health check.

### ESS Connection

The Study Controller calls the Experiment State Server over plaintext in-cluster gRPC (h2c) using Google ID tokens minted via Workload Identity:

- **Address (`-ess_addr`)**: `xmc-bridge` reads `ESS_ADDR` (defaulting to `CMS_ADDRESS` / `grpc-service:50051` in `xmc-bridge-config`) and injects it into every 1P release as `<component>Deployment.essAddr`. Any `essAddr` Component parameter or raw value is stripped (bridge-wins) so a Component CR cannot redirect the controller's Google ID token to an arbitrary host. The chart renders `-ess_addr` with fail-closed `required`.
- **Audience (`-ess_audience`)**: `xmc-bridge` resolves the gRPC audience from `IAP_EXPECTED_GRPC_AUDIENCE` (falling back to `CLIENT_ID` when unset or placeholder) and injects it as `<component>Deployment.essAudience`, stripping any Component override. The chart renders `-ess_audience` with fail-closed `required`.
- **Identity**: `deploy/ctk_v2/xmanager-cloud.yaml` provisions the `xmc-sc-sa` Google service account (no project IAM roles) and binds it to the `xmc-study-controller` Kubernetes ServiceAccount via Workload Identity. Passing `xmc-sc-sa@<project>.iam.gserviceaccount.com` in `parameters.studyControllerServiceAccount` annotates the ServiceAccount and authorizes the controller in ESS.

### Vizier Connection

At startup, `xmc-study-controller` starts its HTTP server (`/healthz` `200 OK`, `/readyz` `503 Service Unavailable`) and polls the live `vizier` `Component` CR in `-components_namespace` using its mounted Kubernetes service account token (`xmc-study-controller-components-reader` `Role`):

- **Resolution & Backoff**: Reads `spec.parameters.endpoint` as a strict `host:port` target. While `vizier` is absent, disabled, malformed, or transiently unreadable from the Kubernetes API, the controller backs off exponentially with jitter (up to `10s`) without crash-looping (`livenessProbe` stays `200 OK`) and holds off starting the Redis stream consumer so queued ticks are not burned into the dead-letter stream.
- **Latched Client & `/readyz` Reachability**: Once resolved, the controller latches a single shared plaintext gRPC connection (`MaxDelay: 5s` reconnect backoff) and starts the Redis consumer. `/readyz` verifies both Redis (`Ping`) and Vizier reachability (`grpc.health.v1.Health/Check`, treating `SERVING` and `codes.Unimplemented` for OSS Vizier as healthy) concurrently within a shared `2s` deadline.
- **Endpoint Changes**: `spec.parameters.endpoint` is latched at startup and not re-resolved per tick; changing the Vizier endpoint requires restarting `xmc-study-controller`.

### Study Reconcile Ticks

The Study Controller consumes the `xmc-study-reconcile-stream` Redis stream as the `study-controller-group` consumer group, sharing a single Redis connection pool across stream consumption, per-study locking, and `/readyz`. Each message is a protojson `StudyReconcilePayload` (`{"experiment": "experiments/{xid}"}`) that names a study; ticks are unsigned hints, delivered at least once.

Each delivered tick is classified by outcome (`xmc_study_controller_reconcile_ticks_total{outcome}`); for valid payloads, the controller acquires an advisory distributed per-study lease at `xmc:study-reconcile-lock:{xid}` (`SET ... NX PX` with a random 16-byte token, 70s lease TTL exceeding the 60s reconcile timeout, and token-checked Lua `DEL`):

- **Acquired (`reconciled`)**: re-reads `experiments/{xid}` from ESS via `GetExperiment` using its Workload Identity Google ID token. Ticks for missing (`NotFound`), non-`STUDY`, or inactive (`status.active_state != ACTIVE`) experiments are logged and acknowledged as no-ops; active `STUDY` experiments are idempotently registered in OSS Vizier (`owners/xmc/studies/study-{xid}`) via `CreateStudy` and acknowledged after releasing the lease.
- **Already held (`contended`)**: logs `Skipped contended study reconcile tick` at info level and acknowledges the tick without reconciling so multi-replica deployments never reconcile the same study concurrently.
- **Lock or transient reconcile failure (`retry_error`)**: releases any acquired lease if still owned and retries the tick via stream redelivery.
- **Invalid study config (`invalid_study`)**: a `StudyConfig` that fails conversion or is rejected by Vizier with `InvalidArgument` (when not cancelled by shutdown) goes directly to `xmc-study-reconcile-dlq-stream` without retrying or logging user payload contents.
- **Malformed payload (`invalid_payload`)**: a payload whose `data` does not decode to a valid `experiments/{xid}` resource name goes to the `xmc-study-reconcile-dlq-stream` dead-letter stream without acquiring the lock or retrying (raw stream entries missing a `data` field are dead-lettered directly by the stream consumer before the handler runs).

The controller re-creates the consumer group when Redis loses it, for example after a restart without persistence.

The Experiment State Server publishes one tick when it creates a `STUDY` experiment; see [Create Experiment](../../../../../docs/flows/experiment_lifecycle.md#1-create-experiment) and [Study Lifecycle](../../../../../docs/flows/study_lifecycle.md) for the end-to-end architecture and tick-processing diagrams.

- **Metrics**: `GET /metrics` exposes `xmc_study_controller_reconcile_ticks_total{outcome="reconciled|contended|retry_error|invalid_payload|invalid_study"}`.
- **Troubleshooting**: to inspect dead-lettered ticks and the reason each was rejected, run `XRANGE xmc-study-reconcile-dlq-stream - +` in `redis-cli`.
- **Shutdown**: on SIGTERM the controller stops its HTTP server (up to 5s), then drains the consumer (up to 10s) so an in-flight tick releases its per-study lock (2s detached timeout) and settles on the stream (5s detached timeout). Ticks it has not started stay pending and are reclaimed later. If you set the Component's `lifecycleControl.drainTimeoutSeconds`, set it to at least 10: the pod's termination grace period is that value plus 5s.

### Image Resolution

The container image is resolved by [`templates/_helpers.tpl`](templates/_helpers.tpl):

1. `hpoDeployment.image`, the image `xmc-bridge` resolves for the release. It is used verbatim when it has a tag or digest; otherwise the tag below is appended.
2. Otherwise `us-docker.pkg.dev/xmc-prod/xmc/study-controller` with `global.imageTag`, else `imageTag`, else `latest`.

`xmc-bridge` resolves `hpoDeployment.image` from the `DEFAULT_HPO_IMAGE` environment variable, set from the umbrella chart value `hpo_custom_image` (the Cluster Toolkit blueprint sets it from `study_controller_image`, `<image_registry>/study-controller-<environment>:<image_tag>`). Without `DEFAULT_HPO_IMAGE`, an explicit `spec.imageTag` resolves to the same default repository, `us-docker.pkg.dev/xmc-prod/xmc/study-controller:<imageTag>`. The image is built by `//cmd/study_controller:study_controller_image`.
