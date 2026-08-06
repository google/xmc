## Summary
Verify and document JobSet `dependsOn` dependency ordering via XMC's existing `KubernetesJob.dependency_order` feature (`b/540788119`).

## Motivation & Context
`startupPolicy` (e.g. `startupPolicyOrder: InOrder`) is deprecated upstream in JobSet `v1alpha2` in favor of explicit `ReplicatedJob.dependsOn` semantics (`dependsOn: [{ name: "pathways-head", status: Ready }]`). XMC already natively supports `dependsOn` via `KubernetesJob.dependency_order` (`job_name`, `job_dependency_state`).

## Key Changes
- **Unit Testing**: Added `TestConstructJobSet_DependencyOrder_PathwaysHeadAndWorker` in `cmd/actuator/k8s_client/jobset_test.go` to explicitly test `pathways-head` and `worker` job dependency ordering translation to `jobsetv1alpha2.DependsOn`.
- **Examples**: Updated `examples/pathways_job/launch.py` to specify `dependency_order` on `worker` job depending on `pathways-head` being `READY`, and updated `examples/pathways_job/README.md`.

## Verification & Testing
- **Commands Executed**: `bazel test //cmd/actuator/k8s_client:k8s_client_test`
- **Coverage & Test Status**: All 10 test iterations passed.

Fixes b/540788119

TAG=agy
CONV=5adde758-c1c4-47b4-ad42-7f872cd18f52
