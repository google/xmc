# Vizier Dev/Test Fixture (`test/e2e/testdata/vizier`)

> [!WARNING]
> **DEV/TEST ONLY.** XMC does not run Vizier in production. `vizier` is a 3P wrapper component:
> operators bring their own Open Source Vizier gRPC service and point
> `components.vizier.parameters.endpoint` at it. This fixture has no authentication, no
> NetworkPolicy and no durable storage.

This directory runs upstream [Open Source Vizier](https://github.com/google/vizier)'s
`DefaultVizierServer` (`google-vizier==0.1.24`) on port 7077 so that KinD tests and GKE dev
environments have an endpoint for the experimental `vizier` wrapper component.

| File | Purpose |
| :--- | :--- |
| [`Dockerfile`](Dockerfile) | Fixture image: Python 3.11 slim (pinned by digest), non-root, port 7077. |
| [`requirements.txt`](requirements.txt) | Exactly pinned direct dependencies. |
| [`vizier_server.py`](vizier_server.py) | Launcher: `DefaultVizierServer` with the algorithm guard, stops gracefully on SIGTERM. |
| [`server_env.py`](server_env.py) | Reads `VIZIER_PORT` (default `7077`) and `VIZIER_DATABASE_URL` (default `sqlite:////data/vizier.db`). |
| [`supported_algorithms.py`](supported_algorithms.py) | Algorithm guard rules. |
| [`vizier.yaml`](vizier.yaml) | Deployment `vizier` and ClusterIP Service `vizier-service:7077`. |

## Build

```bash
docker build -t xmc-vizier:0.1.24 test/e2e/testdata/vizier
```

## Run on KinD

```bash
KIND_CLUSTER="<kind_cluster_name>"
kind load docker-image xmc-vizier:0.1.24 --name "${KIND_CLUSTER}"
kubectl apply -n xmc -f test/e2e/testdata/vizier/vizier.yaml
```

## Run on a GKE Dev Slot

Push the image to a registry the slot can pull from, apply the manifest, then point the Deployment
at the pushed image:

```bash
IMAGE="<region>-docker.pkg.dev/<project_id>/<repository>/xmc-vizier:0.1.24"
docker tag xmc-vizier:0.1.24 "${IMAGE}"
docker push "${IMAGE}"
kubectl apply -n xmc -f test/e2e/testdata/vizier/vizier.yaml
kubectl set image -n xmc deployment/vizier vizier="${IMAGE}"
```

## Register the `vizier` Component

When testing against a profile that includes the experimental `vizier` wrapper component, configure
it with the fixture's endpoint:

```yaml
components:
  vizier:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      endpoint: "vizier-service.xmc.svc.cluster.local:7077"
```

## Supported Algorithms

The image ships only the dependencies of `RANDOM_SEARCH`, `QUASI_RANDOM_SEARCH` and
`GRID_SEARCH`. `CreateStudy` rejects every other algorithm, including an unset one
(`ALGORITHM_UNSPECIFIED`) and `DEFAULT`, with `INVALID_ARGUMENT`.

## Storage

Studies live in SQLite on an `emptyDir` volume: they survive container restarts but are lost
when the pod is deleted or rescheduled.
