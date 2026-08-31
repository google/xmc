# ROD Data Provider Microservice (`rod_data_provider`)

The `rod_data_provider` is a dedicated Go microservice in XManager on Cloud (XMC) that serves real-time and historical queue metrics for the **Resource Observability Dashboard (ROD)**.

---

## Architecture & Responsibilities

1. **Informer & In-Memory Cache (`kueue_cache.go`)**:
   * Uses Kubernetes `controller-runtime` event informers to watch `kueue.x-k8s.io` custom resources (`ClusterQueue`, `LocalQueue`, `ResourceFlavor`).
   * Maintains thread-safe in-memory maps (`ResourceCache[T]` and `KueueCache`) for sub-millisecond capacity queries without putting load on the Kubernetes API server.
2. **Primary Capacity & Submission APIs**:
   * `GetClusterQueues`: Returns current capacity, nominal quotas, used allocations, and cohort borrowing limits across all cluster queues.
   * `GetSubmitQueue`: Resolves target `LocalQueue` and `ClusterQueue` for submitted workloads.
   * `GetFittingQueues`: Filters queues capable of accommodating specified multi-flavor resource requests.
3. **Secondary Historical & Backlog APIs**:
   * `GetRunningAndPendingJobs`: Queries Cloud Spanner for active and queued workloads with AIP-158 keyset pagination.
   * `BatchGetQueueSecondaryData`: Computes sliding-window turnover velocity (jobs/hr, resource/hr) and pending backlog counts.

---

## Build & Test Commands

* **Build the binary**:
  ```bash
  bazel build //cmd/rod_data_provider:rod_data_provider
  ```

* **Run all unit tests**:
  ```bash
  bazel test //cmd/rod_data_provider/...
  ```

* **Build OCI image tarball**:
  ```bash
  bazel build //cmd/rod_data_provider:rod_data_provider_image_tarball
  ```

* **Push container image to Artifact Registry**:
  ```bash
  export XMC_ACR_TARGET=us-docker.pkg.dev/my-project/my-registry
  export XMC_ENV=dev
  export BUILD_TAG=latest
  bazel run //cmd/rod_data_provider:rod_data_provider_image_push
  ```

---

## API Specification

Defined in [`api/protos/rod/api.proto`](../../api/protos/rod/api.proto). Exposes both standard gRPC service endpoints and gRPC-Gateway HTTP JSON routes under `/v1/clusterqueues`.
