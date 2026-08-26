## Root Cause
Previously, Kueue resource resolution relied solely on a single hardcoded node label key (`cloud.google.com/gke-accelerator`) when attempting to resolve generic accelerator resources (such as `nvidia.com/gpu`, `gpu`, or `google.com/tpu`) to specific accelerator names. ClusterQueues using other standard node labels (e.g., `cloud.google.com/gke-gpu-type`, `cloud.google.com/gke-tpu-accelerator`, `cloud.google.com/gke-tpu-topology`, `kueue.x-k8s.io/gpack-gpu`, `gpu.nvidia.com/class`) or relying on the `ResourceFlavor` name itself were not being resolved, resulting in generic resource classification in queue capacity overviews and resource accumulators.

## Implementation & Multi-Label Fallback Logic
1. **Multi-Label Resolution (`cmd/rod_data_provider/kueue_resource_extractor.go`)**:
   - Added `acceleratorLabelKeys` slice containing supported accelerator label keys:
     - `cloud.google.com/gke-accelerator`
     - `cloud.google.com/gke-gpu-type`
     - `cloud.google.com/gke-tpu-accelerator`
     - `cloud.google.com/gke-tpu-topology`
     - `kueue.x-k8s.io/gpack-gpu`
     - `gpu.nvidia.com/class`
   - Updated `resolveResourceType` to iterate through candidate labels in `rf.Spec.NodeLabels`.
   - Added `isSpecificAcceleratorName(name string) bool` helper to fall back to `flavorName` if it specifies a concrete accelerator type (e.g. `nvidia-tesla-h100`, `v5p-8`) while ignoring generic flavor names (`spot`, `ondemand`, `default`).

2. **Generic Resource Normalization (`internal/common/resourceaccumulator/resourceaccumulator.go`)**:
   - Updated `Accumulator` to automatically normalize generic accelerator entries (`nvidia.com/gpu`, `gpu`, `google.com/tpu`, `tpu`) into specific accelerator types when executable resource sets provide concrete accelerator definitions.

## Unit Test Coverage
- Added table-driven test cases in `cmd/rod_data_provider/kueue_resource_extractor_test.go` (`TestResolveResourceType`) covering all candidate label keys, flavor name fallback, generic flavor name exclusions, and non-accelerator resource immutability.
- Added table-driven test cases in `cmd/rod_data_provider/queue_converters_test.go` (`TestBuildQueueOverviewFromClusterQueue_AcceleratorResolution`) for end-to-end QueueOverview resolution.
- Added unit tests in `internal/common/resourceaccumulator/resourceaccumulator_test.go` (`TestNormalizeGenericAccelerators`) verifying normalization of generic GPU/TPU resource types.

TAG=agy
