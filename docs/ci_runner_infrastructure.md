# CI Runner Infrastructure & Bazel Caching Guide

This document outlines the XMC continuous integration infrastructure, the in-cluster Bazel Remote Cache architecture on Google Kubernetes Engine (GKE), and a comprehensive migration playbook for switching between self-hosted ARC runners and standard GitHub-hosted runners (`ubuntu-latest`).

---

## 1. Current Architecture: GitHub-Hosted Runners (`ubuntu-latest`) & Actions Cache

XMC CI workflows execute on standard GitHub-hosted runners (`ubuntu-latest`) with Bazel disk and repository caching managed through `actions/cache` (`~/.cache/bazel-disk` and `~/.cache/bazel-repo`).

### Bazel Configuration (`.bazelrc`)

- CI builds use the `--config=ci` profile in [`.bazelrc`](../.bazelrc):
  ```ini
  build:ci --remote_cache_compression=true
  build:ci --modify_execution_info=GoLink=+no-remote-cache
  test:ci --test_env=TESTCONTAINERS_RYUK_DISABLED=true
  ```
- [`.github/actions/run-backend-tests/action.yml`](../.github/actions/run-backend-tests/action.yml) and [`.github/workflows/e2e-tests.yml`](../.github/workflows/e2e-tests.yml) restore `~/.cache/bazel-repo` and `~/.cache/bazel-disk`, pass `--disk_cache=${HOME}/.cache/bazel-disk` explicitly to Bazel (avoiding unexpanded `~` in `.bazelrc`), and save updated caches on `refs/heads/main`, `merge_group`, or initial cold-start (`cache-matched-key == ''`) runs (while [`.github/workflows/deploy-gke.yml`](../.github/workflows/deploy-gke.yml) restores both caches and configures `--disk_cache=${HOME}/.cache/bazel-disk` before building OCI images).

---

## 2. Optional Self-Hosted Architecture: GKE ARC Runners & In-Cluster Remote Cache

When self-hosted GitHub Actions runners managed by **Actions Runner Controller (ARC)** on GKE are enabled, workflows can target `arc-runner-set` and connect to the in-cluster Bazel Remote Cache.

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

2. **Workflow Concurrency & Stacked PR Diff Scoping**:
   - **Superseded Run Cancellation**: [`.github/workflows/unit-tests.yml`](../.github/workflows/unit-tests.yml) configures `concurrency` (`group: ${{ github.workflow }}-v2-${{ github.event.pull_request.number || github.head_ref || github.run_id }}`, `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`) so rapid force-pushes or stacked PR rebases immediately cancel superseded runs on the same PR while preserving post-submit `push` runs on `main`.
   - **Closest Merge-Base Resolution**: [`.github/actions/run-backend-tests/action.yml`](../.github/actions/run-backend-tests/action.yml) and [`.github/actions/run-ui-tests/action.yml`](../.github/actions/run-ui-tests/action.yml) fetch `origin/main` and `origin/${PR_BASE_REF}` and diff against `PR_HEAD_SHA` (`github.event.pull_request.head.sha` / `github.event.merge_group.head_sha`) rather than GitHub's synthetic merge commit (`refs/pull/<num>/merge`). [`tools/benchmark/resolve_test_scope.py`](../tools/benchmark/resolve_test_scope.py) prioritizes the live target branch (`origin/${PR_BASE_REF}`) and selects the closest ancestor across `origin/${PR_BASE_REF}` (or `PR_BASE_SHA`) and `origin/main` via `git merge-base --independent`.

3. **In-Cluster Bazel Remote Cache (`bazel-remote-cache`)**:
   - Manifest: [`deploy/ci/bazel-remote-cache.yaml`](../deploy/ci/bazel-remote-cache.yaml)
   - Runs `buchgr/bazel-remote-cache` backed by a 100Gi SSD PersistentVolumeClaim (`premium-rwo`).
   - Listens on `grpc://bazel-remote-cache.arc-runners.svc.cluster.local:9092` (gRPC) and `:8080` (HTTP).
   - Provides internal Gigabit/10Gbps cluster networking speeds, eliminating the 2.5–10 minute overhead of downloading/uploading compressed cache tarballs across public GitHub cache APIs.
   - Automatically manages LRU cache eviction at 90 GiB.

4. **ARC Bazel Configuration (`.bazelrc`)**:
   - When targeting `arc-runner-set`, `--config=ci` can be configured to use the in-cluster gRPC remote cache:
     ```ini
     build:ci --remote_cache=grpc://bazel-remote-cache.arc-runners.svc.cluster.local:9092
     build:ci --remote_upload_local_results=false
     build:ci --remote_download_toplevel
     build:ci --experimental_remote_cache_compression=true
     ```
   - Workflows override `--remote_upload_local_results=true` on trusted `refs/heads/main` pushes and `merge_group` runs while keeping `pull_request` runs read-only.
   - `test:ci --test_env=TESTCONTAINERS_RYUK_DISABLED=true` disables the testcontainers Ryuk reaper on CI, because Ryuk startup inside the DinD sidecar intermittently times out. Tests that use testcontainers must terminate their containers explicitly (for example, in `TestMain`), because nothing reaps them until the ephemeral runner pod is deleted.

---

## 3. Migration Reference: Switching Between ARC Runners and GitHub-Hosted Runners (`ubuntu-latest`)

To switch between self-hosted ARC runners and GitHub-hosted cloud runners (`ubuntu-latest`), follow this step-by-step checklist.

### Checklist Summary

- [ ] Update `runs-on` labels across all workflow files in `.github/workflows/`.
- [ ] Adjust Bazel caching strategy in `.bazelrc` and workflows (re-enable `actions/cache` or configure GCS remote cache).
- [ ] Remove ARC/DinD-specific workarounds if any.
- [ ] Scale down or decommission the GKE ARC cluster.

---

### Step 1: Update `runs-on` in Workflow Files

Search and replace `runs-on: 'arc-runner-set'` with `runs-on: 'ubuntu-latest'` across all `.github/workflows/*.yml` files:

- `.github/workflows/auto-approve-absence.yml`
- `.github/workflows/auto-assign-reviewers.yml`
- `.github/workflows/deploy-gke.yml`
- `.github/workflows/e2e-tests.yml`
- `.github/workflows/examples-smoke.yml`
- `.github/workflows/incremental-pr-approval.yml`
- `.github/workflows/lint.yml`
- `.github/workflows/nightly-build.yml`
- `.github/workflows/nightly-examples-e2e.yml`
- `.github/workflows/nightly-visual-snapshot.yml`
- `.github/workflows/unit-tests.yml`

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

1. In `.github/actions/run-backend-tests/action.yml` (and `.github/workflows/e2e-tests.yml`), configure the disk cache restore and save steps around Bazel execution:

```yaml
      - name: 'Restore Bazel Disk Cache'
        id: 'cache-bazel-disk-restore'
        uses: 'actions/cache/restore@d4323d4df104b026a6aa633fdb11d772146be0bf' # v4.2.2
        with:
          path: ~/.cache/bazel-disk
          key: bazel-disk-${{ runner.os }}-${{ github.sha }}
          restore-keys: |
            bazel-disk-${{ runner.os }}-

      - name: 'Save Bazel Disk Cache'
        uses: 'actions/cache/save@d4323d4df104b026a6aa633fdb11d772146be0bf' # v4.2.2
        if: (github.ref == 'refs/heads/main' || github.event_name == 'merge_group' || steps.cache-bazel-disk-restore.outputs.cache-matched-key == '') && steps.cache-bazel-disk-restore.outputs.cache-hit != 'true'
        with:
          path: ~/.cache/bazel-disk
          key: bazel-disk-${{ runner.os }}-${{ github.sha }}
```

2. In `.bazelrc`, remove the in-cluster remote cache under `ci` (since workflows pass `--disk_cache=${HOME}/.cache/bazel-disk` explicitly to avoid unexpanded `~` in `.bazelrc`):
```ini
# build:ci --remote_cache=grpc://bazel-remote-cache.arc-runners.svc.cluster.local:9092
build:ci --remote_cache_compression=true
build:ci --modify_execution_info=GoLink=+no-remote-cache
```

#### Option B: Google Cloud Storage (GCS) Remote Cache — High Performance & Shared Across GitHub Runners

1. Create a regional GCS bucket (e.g. `gs://YOUR_BAZEL_CACHE_BUCKET`).
2. Grant `roles/storage.objectAdmin` to the GitHub Actions Workload Identity provider service account.
3. Authenticate in the workflow using `google-github-actions/auth`.
4. Point `.bazelrc` to GCS:
```ini
build:ci --remote_cache=https://storage.googleapis.com/YOUR_BAZEL_CACHE_BUCKET
build:ci --google_default_credentials=true
build:ci --remote_upload_local_results=true
```

---

### Step 3: Toolchain Setup Actions

GitHub-hosted `ubuntu-latest` virtual environments come with many pre-installed tools. Ensure the following setup actions are present in each job:

- `actions/setup-go@v5` (pins Go version, e.g. `1.26.0`)
- `actions/setup-node@v4` (pins Node version, e.g. `22`)
- `actions/setup-python@v5` (pins Python version, e.g. `3.13`)
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

## 4. Maintenance & Troubleshooting (In-Cluster Remote Cache)

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

## 5. Headless KinD & Playwright End-to-End (E2E) Pipeline

The end-to-end test workflow ([`.github/workflows/e2e-tests.yml`](../.github/workflows/e2e-tests.yml)) spins up an entire ephemeral XMC environment for every merge queue entry (`merge_group`), on manual dispatch (`workflow_dispatch`), and via reusable workflow calls (`workflow_call`). On `pull_request` events targeting `main`, the heavy `execute-e2e` job is skipped via `if: github.event_name != 'pull_request'` while the lightweight `run-e2e` (`KinD & Playwright Headless E2E`) gate job completes with `conclusion: SUCCESS` so pull requests satisfy the required status check to enter the merge queue, where the full E2E suite executes on `merge_group`.

### Architecture & Resource Isolation

```
+-----------------------------------------------------------------------------------------+
| GitHub Actions ARC Runner Pod (or ubuntu-latest)                                        |
|                                                                                         |
|  +-------------------------------------+      +--------------------------------------+  |
|  | Isolated Docker Engine (DinD)       |      | Python E2E Orchestrator              |  |
|  |                                     |      |                                      |  |
|  |  - KinD Cluster (Local)             |      |  1. Provision Spanner DBs & KinD     |  |
|  |    - Core Services (ESS, UI, XID)   |<-----+  2. Build 5 OCI Image Tarballs       |  |
|  |    - Ephemeral Redis & Dex OIDC IdP |      |  3. Load Images into KinD & Helm     |  |
|  |    - Gateway Reverse Proxy (:8080)  |      |  4. Execute Playwright Tests         |  |
|  |    - Dex Ingress (:5556)            |      |  5. Best-Effort Resource Teardown    |  |
|  +-------------------------------------+      +--------------------------------------+  |
|                                                                  |                      |
|                                                                  v (WIF Authenticated)  |
|  +-----------------------------------------------------------------------------------+  |
|  | GCP Cloud Spanner (Configured GCP Project & Spanner Instance)                     |  |
|  |  - Ephemeral DB: e2e-ess-<epoch>-<hex> (Schema from ess-schema.sql)               |  |
|  |  - Ephemeral DB: e2e-xid-<epoch>-<hex> (Schema from xid-schema.sql)               |  |
|  +-----------------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------------+
```

### Multi-Run Concurrency & Collision Prevention

1. **KinD Cluster Isolation**:
   - Each runner pod contains its own isolated Docker daemon. Multiple concurrent E2E runs execute in independent runner pods, meaning local KinD clusters never collide across jobs.
2. **Ephemeral Spanner Database Names & Fast Single-Batch DDL Provisioning**:
   - Each test run generates unique database names using the pattern `e2e-ess-<epoch>-<hex>` and `e2e-xid-<epoch>-<hex>`.
   - Databases are dynamically created in parallel via `gcloud spanner databases create --ddl-file=...` using the source-of-truth schema files (`internal/storage/spanner/schema/ess-schema.sql` and `internal/storage/spanner/schema/xid-schema.sql`), ensuring rapid single-batch DDL compilation and exact parity with Cluster Toolkit deployments, and are cleanly dropped during orchestrator teardown.
3. **Automated Stale Database Garbage Collection**:
   - Before provisioning new databases, `SpannerManager` automatically scans for any leftover databases older than 1 hour and deletes them, preventing orphaned resource accumulation if a runner pod is killed abruptly.

### GitHub Repository Secrets, Environment Protection & Break-Glass

The E2E job references the `e2e-test` deployment environment (`environment: e2e-test`), which does not require manual approval. Access to the cloud credentials is limited by the job condition instead: `if: github.event_name != 'pull_request'` skips the job before a runner or environment secret is accessed on `pull_request` events, so pull request code (including code from forks) reaches the job only after a maintainer queues the pull request into the merge queue, and manual dispatch requires write access to the repository.

If a sustained external infrastructure outage (such as a regional Cloud Spanner incident) causes `KinD & Playwright Headless E2E` to fail in `merge_group` even after the harness's automatic infrastructure retry, a repository administrator can temporarily bypass the gate via **Settings → Rules → Rulesets** by disabling the E2E required status check rule until the outage is resolved.

Workload Identity Federation allows GitHub Actions to authenticate directly to GCP using encrypted GitHub Actions Secrets without hardcoded credentials:

| Secret Name | Description |
| :--- | :--- |
| `secrets.GCP_E2E_PROJECT_ID` | Target GCP project hosting Cloud Spanner (e.g. `YOUR_GCP_PROJECT_ID`). |
| `secrets.GCP_E2E_SPANNER_INSTANCE` | Cloud Spanner instance used for ephemeral databases (e.g. `YOUR_SPANNER_INSTANCE`). |
| `secrets.GCP_E2E_WORKLOAD_IDENTITY_PROVIDER` | Workload Identity Pool provider resource URI (e.g. `projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/POOL_ID/providers/PROVIDER_ID`). |
| `secrets.GCP_E2E_SERVICE_ACCOUNT` | Service account with `roles/spanner.databaseAdmin` (e.g. `sa-name@YOUR_GCP_PROJECT_ID.iam.gserviceaccount.com`). |

### Execution on ARC Self-Hosted vs. GitHub-Hosted (`ubuntu-latest`) Runners

- **On Self-Hosted ARC Runners (`arc-runner-set`)**:
  - The job connects to the in-cluster Bazel Remote Cache (`bazel-remote-cache:9092`), achieving rapid build times.
  - Docker-in-Docker is managed via the runner pod sidecar.
- **On GitHub-Hosted Runners (`ubuntu-latest`)**:
  - Requires `runs-on: 'ubuntu-latest'` in `.github/workflows/e2e-tests.yml`.
  - Docker is natively available.
  - WIF authentication requires the GitHub repository `google/xmc` to be mapped in the GCP Workload Identity Pool.
  - Bazel caching will use the fallback GitHub Actions cache or GCS remote cache as configured in Section 1 and Section 3.

