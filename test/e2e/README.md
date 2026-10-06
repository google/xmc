# E2E Test Suite Orchestration (`test/e2e`)

This directory contains the end-to-end (E2E) integration test framework for XManager on Cloud (XMC). It provisions an ephemeral Kubernetes cluster (via KinD), initializes isolated Cloud Spanner databases, builds and packages all microservice containers, deploys the platform via Helm, and runs Playwright integration scenarios.

---

## Architecture & Concurrency Pipeline (`lib/orchestrator.py`)

The E2E orchestrator organizes setup and teardown into distinct, parallelized execution phases to maximize throughput and minimize overall test duration:

```mermaid
graph TD
    subgraph Phase1["Phase 1: Concurrent Provisioning & Compilation"]
        K["KinD Provisioning<br/>(Cluster Creation, Context & Namespace)"]
        B["Bazel Target Compilation<br/>(//cmd/... containers, proto descriptors & migrations tool)"]
    end

    subgraph Phase2["Phase 2: Concurrent Ingestion & Database Prep"]
        L["KinD Image Loading<br/>(Single-command batch load of all OCI tarballs)"]
        S["Cloud Spanner Initialization<br/>(Concurrent DB creation & direct migrations execution)"]
    end

    subgraph Phase3["Phase 3: Deploy & Test Execution"]
        H["Helm Deployment<br/>(Deploy XMC chart into KinD)"]
        W["Wait for Rollout<br/>(Health check all pods & services)"]
        P["Test Execution<br/>(Virtualenv setup & Playwright E2E scenarios)"]
    end

    subgraph Teardown["Managed Lifecycle Teardown"]
        T["Best-Effort Cleanup<br/>(Drop Spanner DBs, delete KinD cluster & temp dirs)"]
    end

    Phase1 --> Phase2
    Phase2 --> Phase3
    Phase3 -.-> Teardown
```

### Concurrency Phases

1. **Phase 1: Parallel Cluster Spin-Up & Bazel Builds**:
   - `KinD.setup()` creates the local KinD cluster and initializes Kubernetes namespaces and secrets.
   - `ImageBuilder.build_all()` compiles all Go microservice binaries, UI bundles, and the Spanner `migrations` binary concurrently via Bazel.
2. **Phase 2: Parallel Image Ingestion & Spanner Provisioning**:
   - `KinD.load_images()` loads all built container images into KinD nodes in a single batch.
   - `SpannerManager.setup()` creates the `e2e-ess` and `e2e-xid` databases concurrently and applies DDL schemas.
3. **Phase 3: Deployment & Scenario Execution**:
   - `HelmDeployer.deploy()` installs the XMC Helm chart into the cluster and waits for workload readiness.
   - `TestExecutor.run_tests()` invokes the Playwright scenario suite ([test_e2e_flow.py](test_e2e_flow.py)) in an isolated virtualenv.
4. **Teardown (`managed_lifecycle`)**:
   - Handled via a Python context manager that drops the ephemeral Spanner databases and deletes the KinD cluster after a success, a failure, or a stop. Teardown is best effort; see [Stopping & Teardown](#stopping--teardown).

---

## Exit Codes & Infra Retry

The orchestrator tags every failure with the lifecycle phase that raised it ([`lib/phases.py`](lib/phases.py)). Only the phases that provision CI infrastructure are infra phases; the other phases exercise the code under test:

| Phase id | Step | Failure class |
| :--- | :--- | :--- |
| `image-build` | `ImageBuilder.build_all()` | Product |
| `kind-create` | `KindClusterManager.setup()` | Infra |
| `image-load` | `KindClusterManager.load_images()` | Infra |
| `spanner-setup` | `SpannerManager.setup()` | Infra |
| `helm-deploy` | `HelmDeployer.deploy()`, including the wait for workload readiness | Product |
| `tests` | `PlaywrightTestExecutor.execute()`: virtualenv setup, the Dex token fetch, and the Playwright scenarios | Product |

`runner.py` exits with the outcome of its last attempt (`Outcome` in [`lib/outcome.py`](lib/outcome.py)):

| Exit code | Outcome | Meaning |
| :--- | :--- | :--- |
| `0` | Pass | Every phase passed. |
| `1` | Product failure | The `image-build`, `helm-deploy`, or `tests` phase failed. |
| `3` | Infra failure | An infra phase failed, or the [harness deadline](#harness-deadline) expired. |
| `4` | Harness error | An unexpected error outside the lifecycle phases, for example when no `kind` binary can be found. |

Two more exit codes are not outcomes:

- `2`: invalid command-line arguments.
- `128 + N`: stopped by signal `N`, for example `130` for `SIGINT` or `143` for `SIGTERM`, after a best-effort teardown.

> [!IMPORTANT]
> Never retry the job externally, whatever its exit code. The harness already retries an infra failure once within the job timeout, and the other exit codes do not report transient failures.

### Infra Retry

The harness retries an infra failure once ([`lib/infra_retry.py`](lib/infra_retry.py)), but only if the failed attempt, including its teardown, ended within 480 s of the harness start, so that a healthy retry still ends before the harness deadline:

1. Teardown of the failed attempt completes first: its Spanner databases are dropped, and its KinD cluster and temporary directory are deleted.
2. The harness prints a GitHub Actions warning annotation that names the failed phase, for example:

   ```text
   ::warning::E2E infra failure in phase spanner-setup on attempt 1/2; retrying
   ```

3. The retry runs every phase again with fresh Spanner database names and a fresh temporary directory. It skips the image build if the failed attempt finished it. It reuses the KinD cluster name, because `KindClusterManager.setup()` deletes any leftover cluster with that name first.

Each attempt writes its screenshots and diagnostics to its own subdirectory of `--screenshots-dir`: `attempt-1/` and, after a retry, `attempt-2/`. Product failures are never retried. `--keep-cluster` disables the retry, because it skips the teardown that a retry depends on.

### Harness Deadline

900 s after it started, the harness stops itself as described in [Stopping & Teardown](#stopping--teardown) and exits with `3`. Together with the workflow steps before and after the harness, a stopped run still ends more than a minute before the job timeout of 20 minutes.

### Stopping & Teardown

The first `SIGINT` or `SIGTERM` stops the harness ([`lib/cancellation.py`](lib/cancellation.py)): running commands are killed within about 0.5 s, Spanner retry waits and readiness polls end within about 0.5 s, no further attempt starts, and teardown runs. Every command runs in its own session, so the kill reaches the whole process group of the command, for example the `docker` processes that `kind` starts. Processes outside that group, such as the Bazel server and the containers that the Docker daemon runs, are not killed; teardown deletes the KinD cluster and its containers. Further `SIGINT` and `SIGTERM` signals are ignored so that they do not interrupt teardown, and each teardown command is limited to 20 s.

Teardown is best effort. When GitHub Actions cancels a job, or the job times out, the runner sends `SIGINT`, then `SIGTERM` about 7.5 s later, and then `SIGKILL` about 2.5 s after that, so teardown may not finish. The workflow starts `runner.py` with `exec`, so that these signals reach the harness rather than a shell. Spanner databases that a stopped run leaves behind are deleted by the stale-database cleanup of a later run once they are more than one hour old.

---

## Key Performance & Resiliency Optimizations

### 1. Direct Migration Binary Execution (`lib/spanner.py`)
Rather than invoking `bazel run //internal/storage/spanner/migrations:migrations` repeatedly during database setup, the orchestrator compiles the binary once during Phase 1 and invokes the compiled executable directly from `bazel-bin`. This eliminates Bazel startup and analysis overhead, reducing database schema provisioning time from ~100s to ~8s.

### 2. Spanner Transient-Error Backoff (`lib/spanner.py`, `lib/spanner_retry.py`)
Concurrent database creation and schema migrations against Cloud Spanner can hit instance-level DDL queuing, quota limits, and transient server errors. Both operations retry within a fixed budget, which bounds how long Spanner setup can take:

| Operation | Attempts | Waits between attempts | Timeout per attempt | Total cap |
| :--- | :--- | :--- | :--- | :--- |
| Database creation | 4 | 15 s, 30 s, 60 s, each ±20% | 120 s | 240 s |
| Migration version initialization | 3 | 5 s, 10 s | 45 s | 90 s |

No retry starts unless at least 30 s of the total cap remain after its wait, which the timeouts per attempt leave even after a first attempt that timed out. Errors are classified on the command's captured output only, never on its command line:

- Retried within the budget: a command timeout; `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `ABORTED`, or `RESOURCE_EXHAUSTED` as a status name or gRPC code; the plain-text messages `Deadline exceeded`, `unavailable` as a whole word, `Quota exceeded`, `too many concurrent`, and `pending DDL operations`, which `gcloud` can print without a status code; and HTTP 429, 503, or 504 when preceded by an `http`, `status`, or `code` token.
- Retried at most once: `INTERNAL` and `Internal error encountered`, which can be deterministic.
- Never retried: any other error, including `ALREADY_EXISTS` from the first database creation attempt. A retried creation that fails with `ALREADY_EXISTS` may have been preceded by an attempt that timed out after creating the database, so it polls `gcloud spanner databases describe` every 5 s for the rest of the attempt and succeeds once the database is `READY`. A database that is not `READY` by then counts as a command timeout.

Every failed attempt logs the retry decision and the last 2 KB of the command's error output. Because CI logs are public, `projects/<id>` resource paths and project numbers are masked in that output before it is truncated.

### 3. Artifact Registry KinD Node Image Mirror (`lib/kind.py`)
In CI environments, KinD boots using an Artifact Registry mirror image (`us-docker.pkg.dev/.../kindest-node:v1.33.1`) pulled via high-speed GCP internal networks (~15s spin-up). If the mirror image is unreachable or fails to boot, `kind.py` automatically cleans up and falls back gracefully to the Docker Hub upstream image.

### 4. Single-Command Batch Image Loading (`lib/kind.py`)
Loading images concurrently can trigger containerd socket exhaustion and write-lock contention inside the KinD node. The orchestrator batches all container image tarballs into a single `kind load docker-image <img1> <img2> ...` invocation, maximizing containerd import efficiency.

### 5. Pre-Baked Playwright Discovery (`lib/test_executor.py`)
In CI and developer containers where Playwright browser binaries are pre-installed (e.g. at `/ms-playwright` or standard system cache locations), `test_executor.py` automatically detects them and sets `PLAYWRIGHT_BROWSERS_PATH`, avoiding redundant browser downloads.

---

## Running E2E Tests Locally

### Prerequisites
- Docker or Podman
- KinD (`kind`)
- `kubectl` and `helm`
- Python 3.10+
- Google Cloud SDK (`gcloud`) with credentials configured for Spanner access

### Basic Invocations

Run the complete E2E flow:
```bash
python3 test/e2e/runner.py
```

### Useful CLI Flags

- `--keep-cluster`: Prevents cluster teardown on exit for interactive debugging with `kubectl`. Also disables the [infra retry](#infra-retry).
- `--skip-build`: Skips Bazel compilation and reuses existing container images.
- `--custom-kind-node-image <image>`: Overrides the KinD node image (e.g. to test custom Kubernetes versions).
- `--spanner-instance <instance>`: Target specific Spanner instance for testing.

---

## OSS 3P Component KinD E2E Suites (`clickhouse_3p_test.go`, `redpanda_3p_test.go`)

In addition to the full-platform Playwright orchestrator, `test/e2e` includes standalone Go KinD integration suites that verify `xmc-bridge` 3P `Component` CR reconciliation, active health probing, `.status.access.envVars` projection, typed component config resolvers, and live connectivity from mock ML workload pods:

- **ClickHouse 3P E2E Suite** ([clickhouse_3p_test.go](clickhouse_3p_test.go)): Deploys `clickhouse/clickhouse-server:24.3-alpine` inside KinD and validates both operator-managed `wrapperRef` (`ClickHouseInstallation`) discovery and `stable-2026a` operator-less active probing, `componentconfig.ResolveOptions`, and live SQL execution (`CREATE TABLE`, `INSERT`, `SELECT`) from a mock ML workload Pod injected with `.status.access.envVars`.
- **Redpanda 3P E2E Suite** ([redpanda_3p_test.go](redpanda_3p_test.go)): Deploys `docker.redpanda.com/redpandadata/redpanda:v24.1.1` inside KinD and validates both operator-managed `wrapperRef` (`Cluster`) discovery and `stable-2026a` operator-less active probing, `ingestiongateway.ResolveRedpandaComponentConfig`, and live Kafka topic creation, message production, and consumption (`rpk topic create/produce/consume`) from a mock ML workload Pod injected with `.status.access.envVars`.
- **KinD 3P Harness** ([kind_3p_harness_test.go](kind_3p_harness_test.go)): Provisions an isolated ephemeral KinD cluster (or attaches to `KIND_CLUSTER_NAME` when set), preloads container images, installs [crd.yaml](../../deploy/helm/templates/components/crd.yaml), bridges cluster-local service probes over KinD `NodePort`s, and drives in-process CMS and `xmc-bridge` reconciliation.

### Running the 3P Component E2E Suites

```bash
# Run hermetic unit tests for the KinD 3P harness:
bazel test //test/e2e:kind_3p_harness_unit_test

# Run live KinD E2E integration suites (provisions and tears down an ephemeral KinD cluster):
bazel test //test/e2e:clickhouse_3p_test --test_output=streamed
bazel test //test/e2e:redpanda_3p_test --test_output=streamed

# Optionally reuse an existing KinD cluster:
bazel test //test/e2e:clickhouse_3p_test //test/e2e:redpanda_3p_test \
  --test_env=KIND_CLUSTER_NAME=xmc-dev --test_output=streamed
```

