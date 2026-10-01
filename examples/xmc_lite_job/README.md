# XMC Lite Experiment Example (`xm_local.Kubernetes`)

This directory contains an **XMC Lite** example that launches experiments directly on a Google Kubernetes Engine (GKE) cluster using [`xm_local.Kubernetes`](https://github.com/google-deepmind/xmanager/blob/main/xmanager/cloud/kubernetes.py) (`xmanager/cloud/kubernetes.py`), **without** deploying the XMC control plane (no Experiment State Server, no Cloud Spanner/SQL, and no IAP/Envoy ingress).

Each WorkUnit is scheduled directly onto GKE as either a Kubernetes **`JobSet`** (`jobset.x-k8s.io/v1alpha2`, default) or **`Job`** (`batch/v1`) resource (with optional **Kueue** queue admission), and experiment metadata is stored locally in **`~/.xmanager/experiments.sqlite3`**.

---

## 1. Prerequisites

### A. Google Cloud Project
- A Google Cloud Project (`PROJECT_ID`) with billing enabled.
- Required GCP APIs enabled (automatically enabled if using `./setup.sh --provision-cluster`):
  - `container.googleapis.com` (Kubernetes Engine API)
  - `compute.googleapis.com` (Compute Engine API)
  - `iam.googleapis.com` (Identity and Access Management API, provisioning only)
  - `cloudresourcemanager.googleapis.com` (Cloud Resource Manager API, provisioning only)
  - `artifactregistry.googleapis.com` (Artifact Registry API, if pulling private images)

### B. GKE Cluster
Either provisioned automatically via `./setup.sh --provision-cluster` using Cluster Toolkit (`gcluster`), [`xmc-lite-gke.yaml`](xmc-lite-gke.yaml) and [`kueue-queues.yaml`](kueue-queues.yaml), **or** an existing, reachable GKE cluster (`GKE_CLUSTER` in `GCP_REGION`) with:
- **JobSet Controller** (`jobset.x-k8s.io/v1alpha2` CRD installed), required for `--workload_mode=jobset` (the default).
- **Kueue Controller** (`kueue.x-k8s.io` CRDs installed) and a `LocalQueue` in the target namespace, required **only** when using `--local_queue`.

A provisioned cluster gets cert-manager, JobSet and Kueue, plus a `default-flavor` ResourceFlavor, a `cluster-queue` ClusterQueue (6 CPU / 24Gi) and a `default-queue` LocalQueue in `${K8S_NAMESPACE}`.

### C. Local Workstation CLI Tools
- `gcloud` CLI + `gke-gcloud-auth-plugin`
- `kubectl`
- Python 3.11+ with `venv` (`python3 -m venv`)
- `sqlite3` CLI (optional, for inspecting `~/.xmanager/experiments.sqlite3` directly)
- Provisioning only: `terraform` >= 1.12.2, `helm`, and the Cluster Toolkit `gcluster` binary at `./cluster-toolkit/gcluster` (override with `GCLUSTER_BIN`). Build it from source (requires Go and `make`):
  ```bash
  git clone https://github.com/GoogleCloudPlatform/cluster-toolkit.git
  make -C cluster-toolkit
  ./cluster-toolkit/gcluster --version
  ```

---

## 2. Required Permissions

### A. Google Cloud IAM Roles

| Workflow Step | Required GCP IAM Roles on `PROJECT_ID` | Purpose |
| :--- | :--- | :--- |
| **1. Provision GKE Cluster** (`./setup.sh --provision-cluster`) | `roles/container.admin`<br>`roles/compute.networkAdmin`<br>`roles/iam.serviceAccountAdmin`<br>`roles/iam.serviceAccountUser`<br>`roles/resourcemanager.projectIamAdmin`<br>`roles/serviceusage.serviceUsageAdmin` | Enables APIs, creates the VPC network, GKE cluster and node pool, creates the `wl-sa` / `np-sa` service accounts and grants them project roles, and applies the Kueue queues. |
| **2. Run Experiments** (`python3 -m examples.xmc_lite_job.launch`) | `roles/container.developer`<br>`roles/artifactregistry.reader` *(if using private images)* | Fetches `kubeconfig` (`container.clusters.get`) and submits `JobSet` / `Job` objects to GKE. |

### B. Kubernetes RBAC Permissions (in `${K8S_NAMESPACE}`, default `default`)

| API Group | Resources | Verbs | Why It Is Needed |
| :--- | :--- | :--- | :--- |
| `""` (core) | `services` | `get`, `create` | `XmcLiteKubernetesClient._ensure_experiments_service()` reads and creates the headless `experiments` Service for inter-pod DNS resolution. |
| `""` (core) | `pods`, `pods/log` | `get`, `list` | `kubectl get pods` / `kubectl logs` to follow WorkUnit output. |
| `batch` | `jobs` | `get`, `list`, `create`, `delete` | `create` submits workloads in `batch/v1` `Job` mode (`--workload_mode=job`); `delete` is used by the [teardown](#6-teardown) step. |
| `jobset.x-k8s.io` | `jobsets` | `get`, `list`, `create`, `delete` | `create` submits workloads in `JobSet` mode (`--workload_mode=jobset`); `delete` is used by the [teardown](#6-teardown) step. |

---

## 3. Environment Variables (`.env`)

This example uses the same `.env` variable conventions as the root repository.

| Variable | Required in XMC Lite? | Example Value | Description |
| :--- | :---: | :--- | :--- |
| `PROJECT_ID` | **Yes** | `"my-gcp-project"` | Google Cloud Project ID hosting the GKE cluster. |
| `GKE_CLUSTER` | **Yes** | `"xmc-lite-dev-1"` | Name of the GKE cluster. |
| `GCP_REGION` | **Yes** | `"europe-west1"` | GCP region where the GKE cluster resides. |
| `XMANAGER_ENV` | Provisioning only | `"dev-1"` | Environment name used to name the provisioned network resources and isolate `KUBECONFIG`. |
| `K8S_NAMESPACE` | Optional | `"default"` | Kubernetes namespace where `JobSet` / `Job` workloads are submitted. |
| `KUBECONFIG` | Derived | `"${HOME}/Documents/Development/envs/kubeconfig.${XMANAGER_ENV}"` | Isolated kubeconfig path preventing context collisions across worktrees (defaults to `~/.kube/config`). |
| `XMC_BASE_DOMAIN`, `IAP_CLIENT_ID`, `XMC_CLIENT_SA`, `XMANAGER_ENDPOINT` | **No** *(Full XMC only)* | N/A | Required only by Full XMC (`xm_cloud`) for IAP/gRPC ingress; **unused** in XMC Lite. |

```bash
export PROJECT_ID="my-gcp-project"
export GKE_CLUSTER="xmc-lite-dev-1"
export GCP_REGION="europe-west1"
export XMANAGER_ENV="dev-1"
export K8S_NAMESPACE="default"
export KUBECONFIG="${HOME}/Documents/Development/envs/kubeconfig.${XMANAGER_ENV}"
```

---

## 4. Setup & Running the Example

### Step 1: Run `setup.sh`
To provision a new GKE cluster with **JobSet**, **Kueue** and the example queues via `gcluster`, configure `KUBECONFIG`, and create the local Python virtual environment (`.venv`):
```bash
./examples/xmc_lite_job/setup.sh --provision-cluster
```

If your GKE cluster is already running, omit `--provision-cluster` to only configure `KUBECONFIG` and `.venv`:
```bash
./examples/xmc_lite_job/setup.sh
```

### Step 2: Launch an Experiment (`JobSet` or `Job`)
```bash
source .env
source examples/xmc_lite_job/.venv/bin/activate
```

- **Launch as a Kubernetes `JobSet` (`jobset.x-k8s.io/v1alpha2`, default)**:
  ```bash
  python3 -m examples.xmc_lite_job.launch \
    --exp_name="xmc-lite-jobset-demo" \
    --workload_mode=jobset \
    --num_work_units=2 \
    --duration=30
  ```

- **Launch as a `batch/v1` `Job`**:
  ```bash
  python3 -m examples.xmc_lite_job.launch \
    --exp_name="xmc-lite-batch-job-demo" \
    --workload_mode=job \
    --num_work_units=1 \
    --duration=30
  ```

- **Admit through a Kueue `LocalQueue`** (add to either command above):
  ```bash
    --local_queue=default-queue
  ```

- **Dry-Run Mode (generates manifests and writes to local SQLite without contacting GKE)**:
  ```bash
  python3 -m examples.xmc_lite_job.launch \
    --exp_name="xmc-lite-dry-run" \
    --workload_mode=jobset \
    --dry_run
  ```

### Step 3: Inspect the Local SQLite Database & Kubernetes Resources
`~/.xmanager/experiments.sqlite3` is initialized on the first `launch.py` run (`inspect_sqlite_state.py` exits with an error if called before any experiment has been created):
```bash
# Decode and print experiment, work_unit, and job protobuf records from ~/.xmanager/experiments.sqlite3:
python3 -m examples.xmc_lite_job.inspect_sqlite_state

# Or query SQLite directly:
sqlite3 ~/.xmanager/experiments.sqlite3 \
  "SELECT e.experiment_id, e.experiment_title, j.work_unit_id, j.job_name, j.job_data FROM experiment e JOIN job j USING (experiment_id);"

# Inspect workloads on GKE:
kubectl get jobsets,jobs,pods,svc -n "${K8S_NAMESPACE:-default}"
```

### Running the Unit Tests
```bash
python3 -m unittest discover -s examples/xmc_lite_job -p '*_test.py' -t .
```

---

## 5. What Is Created & How It Is Represented

### A. Representation in the Local Database (`~/.xmanager/experiments.sqlite3`)
`xm_local` initializes a local SQLite database at `~/.xmanager/experiments.sqlite3` with 4 tables (`alembic_version`, `experiment`, `work_unit`, `job`):

1. **`experiment`**: `(experiment_id BIGINT PRIMARY KEY, experiment_title VARCHAR(255))`
   - `experiment_id` is a local millisecond Unix epoch timestamp (`int(time.time() * 10**3)`), e.g. `1790367844544`.
2. **`work_unit`**: `(experiment_id BIGINT, work_unit_id INTEGER, PRIMARY KEY (experiment_id, work_unit_id))`
   - Sequential 1-based `work_unit_id` (`1, 2, ...`) per experiment.
3. **`job`**: `(experiment_id BIGINT, work_unit_id INTEGER, job_name VARCHAR(255), job_data VARCHAR(255))`
   - `job_data` stores the text-format serialization of the `xmanager.Job` protobuf (`xmanager.generated.data_pb2`):
     ```textproto
     kubernetes {
       namespace: "default"
       job_name: "xm-1790367844544-1-jobset"
     }
     ```

### B. Representation in Kubernetes (`k8s`)
Both modes create a headless `v1/Service` named `experiments` (`clusterIP: None`, `selector: {service: experiments}`) in `${K8S_NAMESPACE}`.

- **In `jobset` mode**: creates one `jobset.x-k8s.io/v1alpha2` `JobSet` per WorkUnit, named `xm-<experiment_id>-<work_unit_id>-jobset` (normalized to a valid DNS-1035 label $\le 63$ characters with leading/trailing hyphens stripped) with `failurePolicy.maxRestarts: 0`, `replicatedJobs[*].template.spec.backoffLimit: 0`, `restartPolicy: Never`, and optional `kueue.x-k8s.io/queue-name` label.
- **In `job` mode**: creates one `batch/v1` `Job` per `xm.Job` named `<experiment_id>-<work_unit_id>-<job_name>` with `backoffLimit: 0` and `restartPolicy: Never`.

---

## 6. Teardown

```bash
NS="${K8S_NAMESPACE:-default}"

# Delete the example's workloads and the headless Service:
kubectl delete jobsets --all -n "${NS}"
kubectl delete jobs --all -n "${NS}"
kubectl delete svc experiments -n "${NS}"
```

> [!WARNING]
> `--all` removes **every** `JobSet` / `Job` in the namespace. On a shared cluster, delete the example's workloads by name instead (`kubectl get jobsets,jobs -n "${NS}"`).

> [!NOTE]
> `~/.xmanager/experiments.sqlite3` is shared by **all** `xm_local` experiments on this machine, not just this example. Delete it only if you want to wipe all local XManager experiment history.

If the cluster was created with `./setup.sh --provision-cluster`, destroy it (cluster, node pool, network and service accounts) with:
```bash
./cluster-toolkit/gcluster destroy "ctk-out/${GKE_CLUSTER}" --auto-approve
```

> [!CAUTION]
> `gcluster destroy` permanently deletes the GKE cluster and every workload on it. The blueprint sets `deletion_protection: false`, so there is no extra safeguard.

---

## 7. Full XMC (`xm_cloud`) vs. XMC Lite (`xm_local.Kubernetes`) Comparison

### A. Full XMC Script (`xm_cloud` + ESS gRPC `work_unit_pb2.WorkUnit`)
In Full XMC (see [`examples/simple_job/launch.py`](../simple_job/launch.py)), scripts authenticate against the XMC Experiment State Server (ESS) over gRPC/IAP (`XMANAGER_ENDPOINT`) and submit `work_unit_pb2.WorkUnit` protobufs (or use `xm_cloud.KubernetesJobExecutor`):

```python
# Full XMC (examples/simple_job/launch.py pattern)
from xmanager import xm_cloud
from xmanager.vizier.service import resources_pb2
from xmanager.xm_cloud import work_unit_pb2

with xm_cloud.create_experiment(experiment_title="simple-job") as experiment:
    job_proto = work_unit_pb2.Job(
        name="worker",
        kubernetes_job=work_unit_pb2.KubernetesJob(
            image_uri="python:3.11-slim",
            args=["python3", "-c", "print('Hello from Full XMC')"],
            resources=resources_pb2.Resources(cpu=1, memory_bytes=1024**3),
            replica_count=1,
        ),
    )
    wu_proto = work_unit_pb2.WorkUnit(
        scheduling_constraints=work_unit_pb2.SchedulingConstraints(local_queue="default-queue"),
        payload=work_unit_pb2.WorkUnitPayload(jobs=[job_proto]),
    )
    req = work_unit_pb2.CreateWorkUnitRequest(
        parent=experiment.resource_name,
        work_unit=wu_proto,
    )
    experiment._experiment_state_stub.create_work_unit(req)
```

Or using the high-level `xm_cloud` SDK:
```python
# Full XMC (High-level xm_cloud SDK)
from xmanager import xm
from xmanager import xm_cloud

with xm_cloud.create_experiment(experiment_title="simple-job") as experiment:
    [executable] = experiment.package(
        [
            xm.container(
                image_path="python:3.11-slim",
                executor_spec=xm_cloud.KubernetesJobExecutorSpec(),
            )
        ]
    )
    job = xm.Job(
        executable=executable,
        executor=xm_cloud.KubernetesJobExecutor(
            requirements=xm.JobRequirements(cpu=1, ram=1 * xm.GiB),
            replica_retry_limit=0,
        ),
        args=["python3", "-c", "print('Hello from xm_cloud')"],
        name="worker",
    )
    experiment.add(job)
```

### B. XMC Lite Script (`xm_local.Kubernetes`)
In XMC Lite ([`launch.py`](launch.py)), scripts use `xm_local.create_experiment` and `xm_local.Kubernetes` to submit directly to the GKE API server via `KUBECONFIG`:

```python
# XMC Lite (examples/xmc_lite_job/launch.py pattern)
from xmanager import xm
from xmanager import xm_local
from xmanager.xm_local import executables as local_executables

with xm_local.create_experiment(experiment_title="simple-job-lite") as experiment:
    executable = local_executables.GoogleContainerRegistryImage(
        name="worker",
        image_path="python:3.11-slim",
        args=xm.SequentialArgs.from_collection(["python3", "-c", "print('Hello from XMC Lite')"]),
    )
    job = xm.Job(
        executable=executable,
        executor=xm_local.Kubernetes(
            requirements=xm.JobRequirements(cpu=1, ram=1 * xm.GiB),
        ),
        name="worker",
    )
    experiment.add(job)
```

### C. Summary of Differences

| Dimension | Full XMC (`xm_cloud`) | XMC Lite (`xm_local.Kubernetes`) |
| :--- | :--- | :--- |
| **Control Plane Infrastructure** | GKE + Cloud Spanner + Redis + ESS + Actuator + Reconciler + UI + Cert-Manager/IAP | **GKE cluster only** (with JobSet, and optionally Kueue) |
| **Client Authentication** | IAP OAuth (`IAP_CLIENT_ID`, `XMC_BASE_DOMAIN`) | Direct GKE `KUBECONFIG` (`gcloud container clusters get-credentials`) |
| **SDK Module & Experiment Call** | `xm_cloud.create_experiment(experiment_title, acls=..., urls=...)` | `xm_local.create_experiment(experiment_title)` *(no `acls` or `urls`)* |
| **Executor Spec & Executor** | `xm_cloud.KubernetesJobExecutorSpec()` & `xm_cloud.KubernetesJobExecutor(...)` | `xm_local.Kubernetes.Spec()` (or `GoogleContainerRegistryImage`) & `xm_local.Kubernetes(...)` |
| **State & Metadata Storage** | Centralized **Cloud Spanner** database | Local **`~/.xmanager/experiments.sqlite3`** |
| **Kubernetes Resource Created** | `jobset.x-k8s.io/v1alpha2` `JobSet` reconciled by XMC Actuator | Native SDK creates `batch/v1` `Job` + `v1/Service`; `JobSet` created via [`k8s_workload_client.py`](k8s_workload_client.py) |
| **WorkUnits & Artifacts** | Server-side WorkUnit state machine, events, and Artifact storage | WorkUnits exist only as local SQLite keys; XManager Artifacts are **not supported** |
| **Container Packaging Note** | Server-side image reference (`image_uri`) | Upstream `xm_local.packaging.cloud` runs local `docker pull`/`push` to `gcr.io/<project>` unless using `GoogleContainerRegistryImage` directly |

---

## 8. Findings for XMC Lite Requirements

What this example shows about how well the existing XManager SDK (`xm_local.Kubernetes`) covers the XMC Lite user journey:

- **No native JobSet support.** The SDK's built-in client only creates `batch/v1` `Job`s. Submitting `JobSet`s required [`k8s_workload_client.py`](k8s_workload_client.py), which subclasses `xmanager.cloud.kubernetes.Client` and uses `unittest.mock.patch` at runtime to (a) build `V1Job` objects without submitting them and (b) replace the SDK's `client()` factory. This depends on SDK internals and can break on SDK upgrades.
- **Namespace is hard-coded.** The built-in client always uses the `default` namespace; any other namespace needs the custom client.
- **No Kueue integration.** The SDK has no notion of queues; the `kueue.x-k8s.io/queue-name` label is added by the custom client.
- **Experiment state is local only.** Experiments and WorkUnits live in a per-workstation SQLite file, so there is no shared view of experiments across users or machines, and no WorkUnit status tracking beyond the Kubernetes objects themselves.
- **No Artifacts.** XManager Artifacts are not supported in this setup (in line with the agreed scope).
