# CI Runner Infrastructure & Bazel Caching Guide

This document outlines the XMC continuous integration infrastructure, the in-cluster Bazel Remote Cache architecture on Google Kubernetes Engine (GKE), and a comprehensive migration playbook for switching between self-hosted ARC runners and standard GitHub-hosted runners (`ubuntu-latest`).

---

## 1. Current Architecture: Self-Hosted ARC Runners & In-Cluster Remote Cache

XMC CI workflows execute on self-hosted GitHub Actions runners managed by **Actions Runner Controller (ARC)** on GKE.

### Infrastructure Overview

```
+-----------------------------------------------------------------------------------------+
| GKE ARC Cluster                                                                         |
|                                                                                         |
|  +---------------------------+       +-----------------------------------------------+  |
|  | Namespace: arc-systems    |       | Namespace: arc-runners                        |  |
|  |                           |       |                                               |  |
|  |  - gha-rs-controller      |       |  - AutoscalingRunnerSet (arc-runner-set)      |  |
|  |  - gha-rs-listener        |       |    - Runner Pods (arc-runner + dind)          |  |
|  +---------------------------+       |                                               |  |
|                                      |  - Service: bazel-remote-cache:8080 / :9092   |  |
|                                      |  - Deployment: bazel-remote-cache             |  |
|                                      |  - PVC: 100Gi SSD (storageClass: premium-rwo) |  |
|                                      +-----------------------------------------------+  |
+-----------------------------------------------------------------------------------------+
```

### Key Components

1. **ARC AutoscalingRunnerSet (`arc-runner-set`)**:
   - Deployed in the `arc-runners` namespace.
   - Dynamically scales ephemeral runner pods up based on GitHub Actions job queue demand.

2. **In-Cluster Bazel Remote Cache (`bazel-remote-cache`)**:
   - Manifest: [`deploy/ci/bazel-remote-cache.yaml`](../deploy/ci/bazel-remote-cache.yaml)
   - Runs `buchgr/bazel-remote-cache` backed by a 100Gi SSD PersistentVolumeClaim (`premium-rwo`).
   - Listens on `grpc://bazel-remote-cache.arc-runners.svc.cluster.local:9092` (gRPC) and `:8080` (HTTP).
   - Provides internal Gigabit/10Gbps cluster networking speeds, eliminating the 2.5–10 minute overhead of downloading/uploading compressed cache tarballs across public GitHub cache APIs.
   - Automatically manages LRU cache eviction at 90 GiB.

3. **Bazel Configuration (`.bazelrc`)**:
   - CI builds use the `--config=ci` profile:
     ```ini
     build:ci --remote_cache=grpc://bazel-remote-cache.arc-runners.svc.cluster.local:9092
     build:ci --remote_upload_local_results=false
     build:ci --remote_download_toplevel
     build:ci --experimental_remote_cache_compression=true
     ```

---

## 2. Reversion & Migration Playbook: Moving Back to GitHub-Hosted Runners (`ubuntu-latest`)

If the team ever decides to migrate from self-hosted ARC runners back to GitHub-hosted cloud runners (`ubuntu-latest`), follow this step-by-step checklist.

### Checklist Summary

- [ ] Update `runs-on` labels across all workflow files in `.github/workflows/`.
- [ ] Adjust Bazel caching strategy in `.bazelrc` and workflows (re-enable `actions/cache` or configure GCS remote cache).
- [ ] Remove ARC/DinD-specific workarounds if any.
- [ ] Scale down or decommission the GKE ARC cluster.

---

### Step 1: Update `runs-on` in Workflow Files

Search and replace `runs-on: 'arc-runner-set'` with `runs-on: 'ubuntu-latest'` across all `.github/workflows/*.yml` files:

- `.github/workflows/unit-tests.yml`
- `.github/workflows/lint.yml`
- `.github/workflows/terraform-lint.yml`
- `.github/workflows/auto-approve-absence.yml`
- `.github/workflows/auto-assign-reviewers.yml`
- `.github/workflows/block-feature-request-merges.yml`
- `.github/workflows/e2e-tests.yml` (if enabled)

```yaml
# Example change in workflow jobs:
jobs:
  run-backend-tests:
-   runs-on: 'arc-runner-set'
+   runs-on: 'ubuntu-latest'
```

---

### Step 2: Adjust Bazel Caching Strategy

> [!WARNING]
> The internal cluster DNS `http://bazel-remote-cache.arc-runners.svc.cluster.local:8080` is only reachable inside the GKE cluster. GitHub-hosted runners running in GitHub's cloud cannot resolve or access this internal address.

Choose one of the two options below for caching on GitHub-hosted runners:

#### Option A: GitHub Actions Cache (`actions/cache`) — Simple, Zero GCP Infrastructure

1. In `.github/workflows/unit-tests.yml`, re-add the disk cache restore and save steps:

```yaml
      - name: 'Restore Bazel Disk Cache'
        id: 'cache-bazel-disk-restore'
        uses: 'actions/cache/restore@d4323d4df104b026a6aa633fdb11d772146be0bf' # v4.2.2
        with:
          path: ~/.cache/bazel-disk
          key: bazel-disk-${{ runner.os }}-${{ github.sha }}
          restore-keys: |
            bazel-disk-${{ runner.os }}-

      - name: 'Run Backend Unit Tests'
        run: |
          bazel test \
            --repository_cache=~/.cache/bazel-repo \
            --disk_cache=~/.cache/bazel-disk \
            --experimental_repository_cache_hardlinks \
            --test_output=errors \
            --build_tests_only \
            ${RUN_TARGETS}

      - name: 'Save Bazel Disk Cache'
        uses: 'actions/cache/save@d4323d4df104b026a6aa633fdb11d772146be0bf' # v4.2.2
        if: github.ref == 'refs/heads/main' && steps.cache-bazel-disk-restore.outputs.cache-hit != 'true'
        with:
          path: ~/.cache/bazel-disk
          key: bazel-disk-${{ runner.os }}-${{ github.sha }}
```

2. In `.bazelrc`, comment out the in-cluster remote cache:
```ini
# build:ci --remote_cache=http://bazel-remote-cache.arc-runners.svc.cluster.local:8080
```

#### Option B: Google Cloud Storage (GCS) Remote Cache — High Performance & Shared Across GitHub Runners

1. Create a regional GCS bucket (e.g. `gs://xmc-bazel-cache-us-central1`).
2. Grant `roles/storage.objectAdmin` to the GitHub Actions Workload Identity provider service account.
3. Authenticate in the workflow using `google-github-actions/auth`.
4. Point `.bazelrc` to GCS:
```ini
build:ci --remote_cache=https://storage.googleapis.com/xmc-bazel-cache-us-central1
build:ci --google_default_credentials=true
build:ci --remote_upload_local_results=true
```

---

### Step 3: Toolchain Setup Actions

GitHub-hosted `ubuntu-latest` virtual environments come with many pre-installed tools. Ensure the following setup actions are present in each job:

- `actions/setup-go@v5` (pins Go version, e.g. `1.26.0`)
- `actions/setup-node@v4` (pins Node version, e.g. `22`)
- `actions/setup-python@v5` (pins Python version, e.g. `3.14`)
- `bazelbuild/setup-bazelisk@v3` (installs `bazelisk`)

---

### Step 4: Docker & Service Containers

- On GitHub-hosted `ubuntu-latest`, the `runner` user has native rootless/sudo access to the host Docker daemon.
- Service containers (such as `spanner-emulator` in `unit-tests.yml`) run natively without needing `dind` sidecars or GID adjustments.

---

### Step 5: Decommissioning or Pausing the Self-Hosted GKE ARC Cluster

To eliminate cloud resource costs when reverting to GitHub-hosted runners:

1. **Scale down runner set to 0**:
   ```bash
   kubectl scale autoscalingrunnerset arc-runner-set -n arc-runners --replicas=0
   ```
2. **Decommission ARC resources & cluster**:
   Delete or tear down the ARC Helm release and associated GKE cluster resources.

---

## 3. Maintenance & Troubleshooting

### Inspecting Bazel Remote Cache Status
```bash
kubectl get pods,svc,pvc -n arc-runners -l app=bazel-remote-cache
kubectl logs -n arc-runners -l app=bazel-remote-cache --tail=50
```

### Checking Cache Utilization Metrics
The `bazel-remote-cache` service exposes Prometheus and profiling metrics:
```bash
kubectl port-forward svc/bazel-remote-cache -n arc-runners 8080:8080
curl http://localhost:8080/status
```

### Manual Cache Purge
To completely reset the in-cluster cache:
```bash
kubectl delete pod -n arc-runners -l app=bazel-remote-cache
# To wipe the persistent disk entirely:
kubectl delete pvc bazel-remote-cache-pvc -n arc-runners
kubectl apply -f deploy/ci/bazel-remote-cache.yaml
```

---

## 4. Headless KinD & Playwright End-to-End (E2E) Pipeline

The end-to-end test workflow ([`.github/workflows/e2e-tests.yml`](../.github/workflows/e2e-tests.yml)) spins up an entire ephemeral XMC environment on every PR and post-submit run on `main`.

### Architecture & Resource Isolation

```
+-----------------------------------------------------------------------------------------+
| GitHub Actions ARC Runner Pod (or ubuntu-latest)                                        |
|                                                                                         |
|  +-------------------------------------+      +--------------------------------------+  |
|  | Isolated Docker Engine (DinD)       |      | Python E2E Orchestrator              |  |
|  |                                     |      |                                      |  |
|  |  - KinD Cluster: xmc-e2e            |      |  1. Provision Spanner DBs & KinD     |  |
|  |    - Core Services (ESS, UI, XID)   |<-----+  2. Build 5 OCI Image Tarballs       |  |
|  |    - Ephemeral Redis & Dex OIDC IdP |      |  3. Load Images into KinD & Helm     |  |
|  |    - Gateway Reverse Proxy (:8080)  |      |  4. Execute Playwright Tests         |  |
|  |    - Dex Ingress (:5556)            |      |  5. Guaranteed Resource Teardown     |  |
|  +-------------------------------------+      +--------------------------------------+  |
|                                                                  |                      |
|                                                                  v (WIF Authenticated)  |
|  +-----------------------------------------------------------------------------------+  |
|  | GCP Cloud Spanner (Instance: xmc-e2e in project xmc-test-project-3)               |  |
|  |  - Ephemeral DB: e2e-ess-<epoch>-<hex> (Automated Schema Migrations)              |  |
|  |  - Ephemeral DB: e2e-xid-<epoch>-<hex> (Automated Schema Migrations)              |  |
|  +-----------------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------------+
```

### Multi-Run Concurrency & Collision Prevention

1. **KinD Cluster Isolation**:
   - Each runner pod contains its own isolated Docker daemon. Multiple concurrent PR test runs execute in independent runner pods, meaning local KinD clusters named `xmc-e2e` never collide across jobs.
2. **Ephemeral Spanner Database Names**:
   - Each test run generates unique database names using the pattern `e2e-ess-<epoch>-<hex>` and `e2e-xid-<epoch>-<hex>`.
   - Databases are dynamically created via `gcloud`, migrated via `bazel run //internal/storage/spanner/migrations:migrations`, and dropped during orchestrator teardown.
3. **Automated Stale Database Garbage Collection**:
   - Before provisioning new databases, `SpannerManager` automatically scans for any leftover databases older than 2 hours and deletes them, preventing orphaned resource accumulation if a runner pod is killed abruptly.

### GitHub Repository Secrets & Environment Protection

The E2E pipeline runs under the protected environment `environment: e2e-test`. For external/fork PRs, maintainer approval is required before the runner pod is dispatched or cloud tokens are minted.

Workload Identity Federation allows GitHub Actions to authenticate directly to GCP using encrypted GitHub Actions Secrets without hardcoded credentials:

| Secret Name | Description |
| :--- | :--- |
| `secrets.GCP_PROJECT_ID` | Target GCP project hosting Cloud Spanner (e.g. `xmc-test-project-3`). |
| `secrets.GCP_SPANNER_INSTANCE` | Cloud Spanner instance used for ephemeral databases (e.g. `xmc-e2e`). |
| `secrets.GCP_WORKLOAD_IDENTITY_PROVIDER` | Workload Identity Pool provider resource URI (e.g. `projects/815549340089/...`). |
| `secrets.GCP_E2E_SERVICE_ACCOUNT` | Service account with `roles/spanner.databaseAdmin` (e.g. `xmc-e2e-ci@...`). |

### Execution on ARC Self-Hosted vs. GitHub-Hosted (`ubuntu-latest`) Runners

- **On Self-Hosted ARC Runners (`arc-runner-set`)**:
  - The job connects to the in-cluster Bazel Remote Cache (`bazel-remote-cache:9092`), achieving rapid build times.
  - Docker-in-Docker is managed via the runner pod sidecar.
- **On GitHub-Hosted Runners (`ubuntu-latest`)**:
  - Requires `runs-on: 'ubuntu-latest'` in `.github/workflows/e2e-tests.yml`.
  - Docker is natively available.
  - WIF authentication requires the GitHub repository `google/xmc` to be mapped in the GCP Workload Identity Pool.
  - Bazel caching will use the fallback GitHub Actions cache or GCS remote cache as configured in Section 2.

