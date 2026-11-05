# BYO Vizier Reference Install (`deploy/addons/hpo/vizier`)

This Helm chart deploys [Open Source Vizier](https://github.com/google/vizier) as a gRPC service
(`vizier-service:7077`) that XMC consumes as the **`vizier` 3P component** (component class
`optimization-engine`) of the `stable-2026a` profile. XMC does not install Vizier itself: like the
metrics addons under [`deploy/addons/metrics`](../../metrics), this is a bring-your-own (BYO)
reference install that you deploy next to XMC and then register through the profile.

> [!WARNING]
> The Vizier port is **unauthenticated, plaintext gRPC**: anything that can reach it can create,
> read and delete studies. Production installs must set `networkPolicy.enabled=true` (see
> [NetworkPolicy](#4-networkpolicy)) on a cluster whose CNI enforces NetworkPolicies.

The chart renders:

| Resource | Name | Notes |
| :--- | :--- | :--- |
| `Deployment` | `vizier` | One replica, `Recreate` strategy, non-root, read-only root filesystem, TCP readiness/liveness probes on the `grpc` port. |
| `Service` | `vizier-service` | Port `7077` (`grpc`). The fixed name and port match the component's default endpoint. |
| `ServiceAccount` | `vizier` | No Kubernetes API access; token mounting disabled. |
| `PersistentVolumeClaim` | `vizier-data` | SQLite storage, only when `database.type` is `sqlite`. Kept on `helm uninstall`. |
| `NetworkPolicy` | `vizier` | Only when `networkPolicy.enabled` is `true`. |

Every resource is created in the release namespace (`-n xmc` below).

---

## 1. Build the Image

No upstream Vizier server image exists, so build one from [`image/`](image) (see
[`image/README.md`](image/README.md) for what each file does and how to update dependencies). It runs
[`vizier_server.py`](image/vizier_server.py) on Python 3.11 (base image pinned by digest) and
installs the hash-locked dependencies of [`requirements.txt`](image/requirements.txt), compiled
from [`requirements.in`](image/requirements.in) (`google-vizier==0.1.24`). The image tag defaults
to the chart `appVersion`, the pinned google-vizier release:

```bash
docker build -t xmc-vizier:0.1.24 deploy/addons/hpo/vizier/image
```

> [!NOTE]
> Upstream `DefaultVizierServer` only supports SQLite, so the launcher wires the Vizier and
> in-process Pythia servicers onto a SQLAlchemy engine built for the configured backend. On
> PostgreSQL it stores the serialized protos in `BYTEA` columns, runs in `AUTOCOMMIT`, and refuses
> to start when existing Vizier tables keep them in another type. It also refuses to start with
> any google-vizier release other than 0.1.24.

### Supported Algorithms

The image only ships the dependencies of these algorithms:

| Algorithm | Supported |
| :--- | :--- |
| `RANDOM_SEARCH`, `QUASI_RANDOM_SEARCH`, `GRID_SEARCH` | Yes |
| Unset (`ALGORITHM_UNSPECIFIED`), `DEFAULT`, `GAUSSIAN_PROCESS_BANDIT`, and every other algorithm | No |

`CreateStudy` rejects a study with an unsupported algorithm with `INVALID_ARGUMENT`. Clients must
therefore always set one of the supported algorithms explicitly.

### Server Environment

The server reads its configuration from the environment, which the chart sets:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `VIZIER_PORT` | `7077` | gRPC listen port. |
| `VIZIER_ENABLE_REFLECTION` | `false` | Serves gRPC server reflection. |
| `VIZIER_DATABASE_URL` | `sqlite:////data/vizier.db` | SQLAlchemy URL. Takes precedence over `VIZIER_POSTGRES_*`. |
| `VIZIER_POSTGRES_HOST` | *(unset)* | Selects PostgreSQL when `VIZIER_DATABASE_URL` is unset. |
| `VIZIER_POSTGRES_PORT` / `_USER` / `_PASSWORD` / `_DATABASE` / `_SSLMODE` | `5432` / *(required)* / `""` / `vizier` / `verify-full` | PostgreSQL connection. `verify-ca` and `verify-full` trust the image's system CA bundle. |

### Push to Artifact Registry (GKE)

```bash
# Any Artifact Registry Docker repository the cluster can pull from, for example
# <location>-docker.pkg.dev/<project_id>/<repository> (regional or multi-region location).
REGISTRY="<artifact_registry_host>/<project_id>/<repository>"
IMAGE="${REGISTRY}/xmc-vizier:0.1.24"

docker tag xmc-vizier:0.1.24 "${IMAGE}"
docker push "${IMAGE}"
```

### Load into KinD

```bash
KIND_CLUSTER="<kind_cluster_name>"
kind load docker-image xmc-vizier:0.1.24 --name "${KIND_CLUSTER}"
```

---

## 2. Install the Chart

```bash
REGISTRY="<artifact_registry_host>/<project_id>/<repository>"

helm upgrade --install vizier deploy/addons/hpo/vizier -n xmc \
  --set image.repository="${REGISTRY}/xmc-vizier" \
  --set networkPolicy.enabled=true
```

For KinD, use the overlay [`values-kind.yaml`](values-kind.yaml), which keeps the kind-loaded
`xmc-vizier:0.1.24` image, exposes `vizier-service` as a `NodePort`, serves gRPC reflection,
stores studies in SQLite and leaves the NetworkPolicy off:

```bash
helm upgrade --install vizier deploy/addons/hpo/vizier -n xmc \
  -f deploy/addons/hpo/vizier/values-kind.yaml
```

### Values

| Parameter | Default | Description |
| :--- | :--- | :--- |
| `image.repository` / `image.tag` / `image.pullPolicy` | `xmc-vizier` / `""` (chart `appVersion`) / `IfNotPresent` | Server image built in step 1. |
| `service.type` | `ClusterIP` | `vizier-service` type. Its port is always `7077`. |
| `grpcReflection.enabled` | `false` | Serves gRPC server reflection (`true` in `values-kind.yaml`). |
| `database.type` | `sqlite` | `sqlite` or `postgres`. |
| `database.sqlite.storageSize` / `storageClassName` | `1Gi` / `""` | SQLite PVC size and StorageClass (empty uses the cluster default). |
| `database.postgres.host` / `port` / `user` / `database` / `sslMode` | `""` / `5432` / `""` / `vizier` / `verify-full` | PostgreSQL connection; `host` and `user` are required for `postgres`. |
| `database.postgres.passwordSecret.name` / `key` | `postgres-secret` / `postgres-password` | Secret in the release namespace holding the password (required for `postgres`). |
| `networkPolicy.enabled` | `false` | Restricts ingress; required in production, see [NetworkPolicy](#4-networkpolicy). |
| `resources` | 250m / 512Mi requests, 1Gi memory limit | Container resources. |

---

## 3. Persistence

| Option | When to use | Configuration |
| :--- | :--- | :--- |
| **SQLite on a PVC** (default) | Development, KinD, single-cluster trials. | Nothing to set. Studies live in `/data/vizier.db` on the `vizier-data` claim. |
| **PostgreSQL** | Durable installs and backups through your database tooling. | `database.type=postgres` plus the connection values. The database must already exist; Vizier creates its tables on start. |

The Vizier datastore serializes all writes through a single connection, so the chart always runs
one replica.

The `vizier-data` claim carries `helm.sh/resource-policy: keep`, so `helm uninstall` leaves it,
and the studies on it, in place. Reinstall with the same release name (`vizier`) to reuse it, or
delete it first with `kubectl -n xmc delete pvc vizier-data` to start empty; a different release
name fails because the claim still belongs to the old release. Switching `database.type` to
`postgres` also leaves the claim behind, unused.

### Reusing the XMC PostgreSQL Server

XMC does not deploy PostgreSQL. When XMC itself runs on its experimental PostgreSQL backend
(`database.backend=postgres` with `database.experimentalPostgres=true`), its chart creates the
Secret `postgres-secret` (key `postgres-password`) in the release namespace from
`database.postgres.password`. Vizier can use the same server and credentials with a **separate
database** so that it never touches the XMC schemas:

```bash
POSTGRES_HOST="<database.postgres.host of the XMC release>"
POSTGRES_USER="<database.postgres.user of the XMC release>"

# Create the Vizier database once, with any PostgreSQL client that can reach the server:
psql "host=${POSTGRES_HOST} user=${POSTGRES_USER} dbname=postgres sslmode=verify-full" \
  -c 'CREATE DATABASE vizier;'

helm upgrade --install vizier deploy/addons/hpo/vizier -n xmc \
  --set image.repository="<image_repository>" \
  --set networkPolicy.enabled=true \
  --set database.type=postgres \
  --set database.postgres.host="${POSTGRES_HOST}" \
  --set database.postgres.user="${POSTGRES_USER}"
```

`database.postgres.passwordSecret` defaults to `postgres-secret` / `postgres-password`, so the
password is read from the XMC Secret and never appears in the Vizier values. Point it at another
Secret to use dedicated credentials.

> [!NOTE]
> With `sslMode` `verify-ca` or `verify-full`, the server certificate must chain to a CA in the
> image's system bundle. Servers signed by a private CA (for example a per-instance CA) are not
> supported yet: the chart has no value to mount a custom root certificate.

---

## 4. NetworkPolicy

With `networkPolicy.enabled=true`, the chart admits ingress to the Vizier `grpc` port only from pods
in the release namespace that carry one of these labels:

| Label | Client |
| :--- | :--- |
| `app: xmc-bridge` | The `xmc-bridge` TCP health probe that marks the `vizier` component Ready. |
| `xmc.google.com/vizier-client: "true"` | Workloads allowed to call Vizier. |

The policy has no `namespaceSelector`, so it only admits these pods when they run in the same
namespace as Vizier. It is off by default, and in `values-kind.yaml` because KinD's default CNI
does not enforce it; enable it on every production install, on a CNI that enforces
NetworkPolicies (for example GKE Dataplane V2).

Clusters that do not enforce NetworkPolicies, which includes many development clusters, accept the
policy but do not apply it. On GKE, check enforcement with:

```bash
CLUSTER="<cluster>"
LOCATION="<location>"
gcloud container clusters describe "${CLUSTER}" --location "${LOCATION}" \
  --format='value(networkConfig.datapathProvider,networkPolicy.enabled)'
```

Enforced when the datapath is `ADVANCED_DATAPATH`, or when `networkPolicy.enabled` is `True`.

---

## 5. Register the `vizier` Component

With XMC deployed with `enable_components=true`, enable the component in the `stable-2026a`
profile. See the [Vizier component blueprint](../../../profiles/stable-2026a/components/vizier/README.md)
for every field:

```yaml
components:
  vizier:
    enabled: true
    acknowledgeExperimental: true
    parameters:
      # Default; change it when Vizier runs in a namespace other than xmc.
      endpoint: "vizier-service.xmc.svc.cluster.local:7077"
```

`xmc-bridge` then probes `parameters.endpoint` over TCP and reports the component `Ready` once the
Vizier server accepts connections. The bridge does not log successful probes; read the result from
the component status (`kubectl -n xmc get components`). When Vizier becomes unreachable, the
component turns `Ready=False` with reason `ProbeUnhealthy` within about one probe interval.

---

## 6. Verify

```bash
kubectl -n xmc rollout status deployment/vizier
kubectl -n xmc port-forward service/vizier-service 7077:7077
```

`grpcurl` needs either gRPC reflection, which is on in `values-kind.yaml` and off by default, or
the Vizier proto files. With reflection on, list the services and query the (initially empty)
studies of an owner in a second terminal. Vizier answers `ListStudies` for an owner that has no
studies yet with an "Owner name ... does not exist" error.

```bash
grpcurl -plaintext localhost:7077 list
grpcurl -plaintext -d '{"parent": "owners/xmc"}' localhost:7077 vizier.VizierService/ListStudies
```

`list` prints `vizier.VizierService` and `grpc.reflection.v1alpha.ServerReflection`. With
reflection off, `list` fails with "server does not support the reflection API"; pass the protos
from the google-vizier package instead (`-import-path` and `-proto`), or enable reflection
temporarily with `--set grpcReflection.enabled=true`.
