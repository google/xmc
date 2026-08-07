## Summary
Adds support for configurable `restartPolicy` on `KubernetesJob` (e.g. `OnFailure` for worker pods vs `Never` for head pods).

## Motivation & Context
In multi-host workloads and multi-replica job configurations, head and worker pods may require different pod restart policies. Supporting configurable `restart_policy` in `KubernetesJob` enables setting `OnFailure` on worker pods while maintaining `Never` on head pods.

## Key Changes
- **Proto Definition** (`api/protos/experiment/work_unit.proto`): Added `optional string restart_policy = 18;` to `KubernetesJob`.
- **JobSet Actuator** (`cmd/actuator/k8s_client/jobset.go`): Configured `PodSpec.RestartPolicy` in `k8sJobToReplicatedJob` using `job.GetRestartPolicy()`, defaulting to `corev1.RestartPolicyNever` when omitted.
- **Contract Validation** (`cmd/actuator/k8s_client/jobset.go`): Updated `validateNonRestartingJobSetContract` to accept `OnFailure` and `Never` restart policies.
- **Unit Tests** (`cmd/actuator/k8s_client/jobset_test.go`): Added `TestConstructJobSet_RestartPolicy` testing default (unset), explicit `"Never"`, and explicit `"OnFailure"` options. Updated contract validation tests.

## Verification & Testing
- **Commands Executed**: `bazel test //cmd/actuator/k8s_client/...` and `bazel coverage --combined_report=lcov //cmd/actuator/k8s_client/...`
- **Coverage & Test Status**: All 17 targets and 10 test shards passed cleanly. Repository pre-commit hooks (`./.githooks/pre-commit`) passed.

## Related Issues / Bugs
Fixes bug 540787951

TAG=agy
CONV=edd68cb7-6cfc-4970-a305-6ffb0c75b73c
