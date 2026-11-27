# MLflow Dev/Test Fixture (`test/e2e/testdata/mlflow`)

> [!WARNING]
> **DEV/TEST ONLY — NOT FOR PRODUCTION.** XMC does not run MLflow in production. `mlflow` is a 3P wrapper component: operators bring their own MLflow 3.x tracking server and point `components.mlflow.parameters.trackingUri` at it. This fixture has no authentication, no TLS, no NetworkPolicy, and no durable storage.

This directory runs an upstream MLflow 3.x tracking server (`mlflow==3.1.1`) on port 5000 so that KinD tests and dev environments have an endpoint for the experimental `mlflow` wrapper component of the [`stable-2026b` profile](../../../../deploy/profiles/stable-2026b/README.md).

| File | Purpose |
| :--- | :--- |
| [`Dockerfile`](Dockerfile) | Fixture image: Python 3.11 slim (pinned by digest), non-root, port 5000. |
| [`requirements.txt`](requirements.txt) | Pinned MLflow 3.x dependency. |
| [`mlflow.yaml`](mlflow.yaml) | Deployment `mlflow` and ClusterIP Service `mlflow-service:5000` with `/health` probes. |

## Build

```bash
docker build -t xmc-mlflow:3.1.1 test/e2e/testdata/mlflow
```

## Run on KinD

```bash
KIND_CLUSTER="<kind_cluster_name>"
kind load docker-image xmc-mlflow:3.1.1 --name "${KIND_CLUSTER}"
kubectl apply -n xmc -f test/e2e/testdata/mlflow/mlflow.yaml
```

## Register the `mlflow` Component

Enable the component in the `stable-2026b` profile with the fixture's URI (see the [component blueprint](../../../../deploy/profiles/stable-2026b/components/mlflow/README.md)):

```yaml
components:
  mlflow:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      trackingUri: "http://mlflow-service.xmc.svc.cluster.local:5000"
```

## Storage

Runs and artifacts live in SQLite and `/data/artifacts` on an `emptyDir` volume: they survive container restarts but are lost when the pod is deleted or rescheduled.
