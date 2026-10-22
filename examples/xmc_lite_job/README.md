# XMC Lite Experiment Example (`xm_local.Kubernetes`)

This directory contains an **XMC Lite** example that launches experiments directly on a Google Kubernetes Engine (GKE) cluster using [`xm_local.Kubernetes`](https://github.com/google-deepmind/xmanager/blob/main/xmanager/cloud/kubernetes.py) (`xmanager/cloud/kubernetes.py`), **without** deploying the XMC control plane (no Experiment State Server, no Cloud Spanner/SQL, and no IAP/Envoy ingress).

Each WorkUnit is scheduled directly onto GKE as a Kubernetes **`Job`** (`batch/v1`) by the XManager SDK's built-in Kubernetes client, and experiment metadata is stored locally in **`~/.xmanager/experiments.sqlite3`**.

---

## 1. Prerequisites

### A. Google Cloud Project
- A Google Cloud Project (`PROJECT_ID`) with billing enabled.
- Required GCP APIs enabled:
  - `container.googleapis.com` (Kubernetes Engine API)
  - `compute.googleapis.com` (Compute Engine API)
  - `artifactregistry.googleapis.com` (Artifact Registry API, if pulling private images)

### B. GKE Cluster
- An existing, reachable GKE cluster (`GKE_CLUSTER` in `GCP_REGION`). No extra controllers are required for `batch/v1` `Job` workloads.

### C. Local Workstation CLI Tools
- `gcloud` CLI + `gke-gcloud-auth-plugin`
- `kubectl`
- Python 3.11+ with `venv` (`python3 -m venv`)
- `sqlite3` CLI (optional, for inspecting `~/.xmanager/experiments.sqlite3` directly)

---

## 2. Required Permissions

### A. Google Cloud IAM Roles

| Workflow Step | Required GCP IAM Roles on `PROJECT_ID` | Purpose |
| :--- | :--- | :--- |
| **Run Experiments on Existing Cluster** (`python3 -m examples.xmc_lite_job.launch`) | `roles/container.developer`<br>`roles/artifactregistry.reader` *(if using private images)* | Fetches `kubeconfig` (`container.clusters.get`) and submits `Job` objects to GKE. |

### B. Kubernetes RBAC Permissions (namespace `default`)

| API Group | Resources | Verbs | Why It Is Needed |
| :--- | :--- | :--- | :--- |
| `""` (core) | `services` | `list`, `create` | The SDK lists Services and creates the headless `experiments` Service for inter-pod DNS resolution. |
| `""` (core) | `pods`, `pods/log` | `get`, `list` | `kubectl get pods` / `kubectl logs` to follow WorkUnit output. |
| `batch` | `jobs`, `jobs/status` | `get`, `create`, `delete` | `create` submits one `Job` per WorkUnit, `get` on `jobs/status` lets the SDK wait for completion, and `delete` is used by the [teardown](#6-teardown) step. |

---

## 3. Environment Variables (`.env`)

This example uses the same `.env` variable conventions as the root repository.

| Variable | Required in XMC Lite? | Example Value | Description |
| :--- | :---: | :--- | :--- |
| `PROJECT_ID` | **Yes** | `"my-gcp-project"` | Google Cloud Project ID hosting the GKE cluster. |
| `GKE_CLUSTER` | **Yes** | `"xmc-lite-dev-1"` | Name of the GKE cluster. |
| `GCP_REGION` | **Yes** | `"europe-west1"` | GCP region where the GKE cluster resides. |
| `XMANAGER_ENV` | Optional | `"dev-1"` | Environment name, handy for keeping one `KUBECONFIG` per environment (see below). |
| `KUBECONFIG` | Optional | `"${HOME}/.kube/config.${XMANAGER_ENV}"` | Kubeconfig file written by `setup.sh` and used by `launch.py` and `kubectl`. Defaults to `~/.kube/config`. |
| `XMC_BASE_DOMAIN`, `IAP_CLIENT_ID`, `XMC_CLIENT_SA`, `XMC_AUTH_TOKEN`, `XMANAGER_ENDPOINT` | **No** *(Full XMC only)* | N/A | Required only by Full XMC (`xm_cloud`) for IAP/gRPC ingress and [authentication](../../docs/authentication.md); **unused** in XMC Lite. |

Example `.env` (same `KEY="VALUE"` format as the root [`.env.example`](../../.env.example)):
```bash
PROJECT_ID="my-gcp-project"
GKE_CLUSTER="xmc-lite-dev-1"
GCP_REGION="europe-west1"
XMANAGER_ENV="dev-1"
KUBECONFIG="${HOME}/.kube/config.${XMANAGER_ENV}"
```

---

## 4. Setup & Running the Example

### Step 1: Run `setup.sh`
Configure `KUBECONFIG` for the existing cluster and create the local Python virtual environment (`.venv`):
```bash
./examples/xmc_lite_job/setup.sh
```

### Step 2: Launch an Experiment
Load `.env` with `allexport` (as in [`INSTALL.md`](../../INSTALL.md)) so `KUBECONFIG` also reaches `kubectl` in the later steps:
```bash
set -o allexport; source .env; set +o allexport
source examples/xmc_lite_job/.venv/bin/activate

python3 -m examples.xmc_lite_job.launch \
  --exp_name="xmc-lite-batch-job-demo" \
  --num_work_units=2 \
  --duration=30
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
kubectl get jobs,pods,svc -n default
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
       job_name: "1790367844544-1-worker"
     }
     ```

### B. Representation in Kubernetes (`k8s`)
- Creates a headless `v1/Service` named `experiments` (`clusterIP: None`, `selector: {service: experiments}`).
- Creates one `batch/v1` `Job` per `xm.Job` named `<experiment_id>-<work_unit_id>-<job_name>` with `backoffLimit: 0` and `restartPolicy: Never`.
- The SDK's built-in client always uses the `default` namespace.

---

## 6. Teardown

```bash
# Delete the example's workloads and the headless Service:
kubectl delete jobs --all -n default
kubectl delete svc experiments -n default
```

> [!WARNING]
> `kubectl delete jobs --all` removes **every** `Job` in the namespace. On a shared cluster, delete the example's jobs by name instead (`kubectl get jobs -n default`).

> [!NOTE]
> `~/.xmanager/experiments.sqlite3` is shared by **all** `xm_local` experiments on this machine, not just this example. Delete it only if you want to wipe all local XManager experiment history.
