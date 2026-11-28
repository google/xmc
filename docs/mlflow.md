<!--
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# MLflow Tracking Integration (Experimental)

XMC integrates with an external **MLflow 3.x** tracking server so researchers can log metrics with standard `mlflow` calls while XMC groups runs by launch, links both UIs together, and keeps run statuses in sync even if a pod is killed, preempted, or stopped.

## How it works

1. **Plain MLflow code**: Training scripts call standard `mlflow.start_run()` and `mlflow.log_metric(...)`. XMC injects `MLFLOW_TRACKING_URI`, `MLFLOW_EXPERIMENT_ID`, `MLFLOW_RUN_ID`, and configured credentials into every container of each `STANDARD` WorkUnit created while the `mlflow` component is enabled and `Ready`.
2. **Runs grouped by launch**: XMC maps each XMC experiment title to an MLflow experiment (get-by-name or create), creates one parent run named `xmc-<xid>`, and creates one child run named `wu-<wid>` (with `mlflow.parentRunId`) per WorkUnit. Runs carry `xmc.experiment_id`, `xmc.work_unit_id`, `xmc.execution_id`, `xmc.experiment_url`, and `xmc.work_unit_url` tags.
3. **Links both ways**: XMC attaches `MLflow charts` (filtered by `xmc.experiment_id` with `compareRunsMode=CHART`) to `Experiment.urls` and `MLflow run` to `WorkUnit.urls`. MLflow runs link back to XMC via `xmc.experiment_url`, `xmc.work_unit_url`, and the Markdown description tag `mlflow.note.content`.
4. **Status that follows XMC**: XMC sets each child run to `SCHEDULED` at creation and on restart; `mlflow.start_run()` moves it to `RUNNING`. On terminal transitions, XMC writes `COMPLETED` -> `FINISHED`, `FAILED` -> `FAILED`, and `STOPPED` -> `KILLED` (with `end_time`) server-side, overriding whatever the training process wrote on exit.

## Admin setup

XMC does not install or upgrade MLflow in production; operators bring their own MLflow 3.x server and connect it through the `mlflow` component in the [`stable-2026b` profile](../deploy/profiles/stable-2026b/README.md) (`stable-2026a` is locked and does not include `mlflow`).

### 1. Enable the `mlflow` component

See [the `mlflow` component README](../deploy/profiles/stable-2026b/components/mlflow/README.md) for the full parameter schema. Because `mlflow` has `stability: EXPERIMENTAL`, enabling it requires `acknowledgeExperimental: true`:

```yaml
components:
  mlflow:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      trackingUri: "https://mlflow.internal.example.com"
      uiBaseUrl: "https://mlflow.example.com" # optional; defaults to trackingUri
      secretName: "mlflow-credentials"        # optional; omit for unauthenticated servers
      secretTokenKey: "token"                 # or secretUsernameKey / secretPasswordKey
      healthCheckEndpoint: "https://mlflow.internal.example.com/healthz" # defaults to <trackingUri>/health
```

`xmc-bridge` probes `healthCheckEndpoint` with an unauthenticated HTTP `GET`. If your tracking server requires authentication on `/health`, point `healthCheckEndpoint` at an unauthenticated health endpoint.

### 2. Local KinD quickstart (dev/test fixture)

For local evaluation on KinD, deploy the dev/test-only MLflow fixture in [`test/e2e/testdata/mlflow`](../test/e2e/testdata/mlflow/README.md):

```bash
docker build -t xmc-mlflow:3.1.1 test/e2e/testdata/mlflow
kind load docker-image xmc-mlflow:3.1.1 --name "<kind_cluster_name>"
kubectl apply -n xmc -f test/e2e/testdata/mlflow/mlflow.yaml
```

Then enable `components.mlflow` in `stable-2026b` with `trackingUri: "http://mlflow-service.xmc.svc.cluster.local:5000"` (no `secretName`). Once `kubectl get component mlflow -n xmc` reports `Ready`, launch [`examples/mlflow_job`](../examples/mlflow_job/README.md) and verify with [`mlflow_job_test.py`](../tools/nightly_tests/examples_verification/mlflow_job_test.py).

## Researcher workflow

Your launch script needs **no MLflow code or imports**. Submit a `STANDARD` experiment as usual (see [`examples/mlflow_job/launch.py`](../examples/mlflow_job/launch.py)), and call standard `mlflow` APIs in your training container (see [`examples/mlflow_job/worker.py`](../examples/mlflow_job/worker.py)):

```python
import mlflow

with mlflow.start_run():
    mlflow.log_params({"learning_rate": 0.01, "seed": 1})
    mlflow.log_metric("loss", 0.42, step=1)
```

With `MLFLOW_RUN_ID` injected by XMC, `mlflow.start_run()` resumes the pre-created child run automatically.

## Restart, stop, and failure behavior

- **Restarts**: Restarting a WorkUnit creates a new `WorkUnitExecution` that reuses the same MLflow child run (`wu-<wid>`), updates `xmc.execution_id`, resets the child status to `SCHEDULED`, and reopens a terminal parent run (`xmc-<xid>`) to `RUNNING`.
- **Stopping an experiment**: Stopped WorkUnits are marked `KILLED` in MLflow. WorkUnits that already finished (`COMPLETED`) remain `FINISHED`. The parent run transitions to `KILLED` if every child run is `KILLED`, `FAILED` if every child is `FAILED`, or `FINISHED` otherwise.
- **Non-blocking failure handling**: MLflow is never on XMC's critical path. Slow or unreachable MLflow servers do not block `CreateExperiment`, `CreateWorkUnit`, or WorkUnit state persistence; XMC records a `WARNING` status message and retries terminal status updates with backoff. Credentials never appear in logs, status messages, URLs, or tags.

## Limits and caveats

- **Experimental stability**: `mlflow` is marked `EXPERIMENTAL` in `deploy/profiles/stable-2026b`; behavior and configuration fields may evolve.
- **Multi-replica WorkUnits**: All containers in a WorkUnit share the same `MLFLOW_RUN_ID`. In multi-replica or multi-process jobs, only one rank (for example, rank 0) should call `mlflow.start_run()` and log metrics.
- **MultiKueue & Queue Namespaces**: Even on a single-cluster XMC install, JobSets run in the Kueue queue namespaces (for example `cpu-queue`, `gpu-queue`, `tpu-queue`); in MultiKueue deployments, training pods may also run on remote worker clusters. `trackingUri` must be network-reachable from both the XMC control plane and all worker clusters, and when `secretName` is configured, that `Secret` must exist in both the XMC control-plane namespace (`xmc`) and every queue namespace where training pods run (XMC does not replicate Secrets across namespaces or clusters).
- **Opt-out**: Every `STANDARD` experiment created while `mlflow` is enabled and `Ready` receives MLflow runs; per-experiment opt-out is not yet supported.
