## Summary
Stamps the XMC `experiment_id` label (along with `workunit_id` and `created_by`) onto `WorkloadDetails` for ManagedXprof MLRuns, and ensures metadata labels are consistently preserved during post-creation update and conflict upsert.

## Motivation & Context
Resolves item 4 of #849. When the reconciler provisions an MLRun for ManagedXprof execution, `opts.WorkloadDetails.Labels` was previously unpopulated, and post-creation `UpdateMLRun` passed only `list_existing_sessions_only: "true"`. This risked omitting or dropping the originating experiment ID on GKE workload details and MLRun metadata. Attaching these labels allows Cloud Console, Diagon, and downstream profilers to correlate the workload directly with its XMC experiment.

## Key Changes
- **WorkloadDetails Labels**: Stamped `labels` (containing `experiment_id`, `workunit_id`, `created_by`) onto `hypercomputeclient.GKEWorkloadDetails.Labels`.
- **Helper Extraction**: Extracted `buildMLRunUpdateLabels` to combine base metadata labels with `list_existing_sessions_only: "true"` cleanly across both post-creation update and conflict upsert flows.
- **Idempotent Upsert Verification**: Updated `upsertExistingMLRun` to check that existing runs carry the matching `experiment_id` before treating them as an idempotent success.
- **Test Coverage**: Updated unit tests in `cmd/reconciler/jobset/reconciler_test.go` to assert `WorkloadDetails.Labels` and update label preservation, and added table-driven test `TestBuildMLRunUpdateLabels`.

## Assumptions
None — requirements were fully specified.

## Verification & Testing
- **Commands Executed**:
  - `bazel test //cmd/reconciler/jobset/... //cmd/reconciler/hypercomputeclient/...`
  - `bazel test //cmd/reconciler/...`
  - `./.githooks/pre-commit`
  - `./tools/analyze_diff_size.py --base-ref origin/main...`
- **Coverage & Test Status**: All 7 reconciler test targets passed cleanly. Diff size analysis confirmed 0 threshold violations (`should_trigger_split_verifier: false`).

## Related Issues / Bugs
Part of #849

TAG=agy
CONV=38d3c465-a9ec-4546-9618-390a6bba10fe
