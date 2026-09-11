## Summary
Stamps the canonical XMC `experiment_url` deep-link label (along with `workunit_id` and `created_by`) onto MLRun resources and `WorkloadDetails` for ManagedXprof MLRuns, and ensures metadata labels are consistently preserved during post-creation update and conflict upsert.

## Motivation & Context
Resolves item 4 of #849. Diagon and Cloud Console MLRun views require a canonical deep-link URL (`experiment_url`, e.g. `https://<domain>/experiments/<xid>` or `/experiments/<xid>`) rather than a raw numeric ID so operators can navigate directly to the XMC experiment from Cloud Console. Additionally, when the reconciler provisions an MLRun for ManagedXprof execution, `opts.WorkloadDetails.Labels` was previously unpopulated, and post-creation `UpdateMLRun` passed only `list_existing_sessions_only: "true"`. This PR migrates the label key to `experiment_url`, populates `WorkloadDetails.Labels`, and ensures the full label set is preserved across creates, updates, and conflict upserts.

## Key Changes
- **Configuration & Resolution**: Added `ui_base_url` CLI flag and env config (`XMC_UI_URL`, `XMC_DOMAIN` fallback) to `Config` and `jobset.Params`.
- **Pure Helpers**:
  - `constructExperimentURL`: Canonicalizes base URLs, normalizes schemes/slashes, and builds `/experiments/<xid>` deep-links.
  - `resolveUIBaseURL`: Encapsulates configuration precedence across flags and environment variables.
  - `buildMLRunUpdateLabels`: Merges metadata labels with `list_existing_sessions_only: "true"` cleanly across post-creation updates and conflict upserts.
- **WorkloadDetails Labels**: Stamped `labels` (containing `experiment_url`, `workunit_id`, `created_by`) onto `hypercomputeclient.GKEWorkloadDetails.Labels`.
- **Idempotent Upsert Verification**: Updated `upsertExistingMLRun` to check that existing runs carry the matching `experiment_url` before treating them as an idempotent success.
- **Test Coverage**: Updated unit tests in `cmd/reconciler/jobset/reconciler_test.go` to assert `experiment_url` across creates, updates, and upserts; added table-driven unit tests for `constructExperimentURL`, `resolveUIBaseURL`, and `buildMLRunUpdateLabels`.

## Assumptions
None — requirements were fully specified.

## Verification & Testing
- **Commands Executed**:
  - `bazel test //cmd/reconciler/...`
  - `./.githooks/pre-commit`
  - `./tools/analyze_diff_size.py --base-ref origin/main...`
- **Coverage & Test Status**: All 7 reconciler test targets passed cleanly. Diff size analysis confirmed 0 threshold violations (`should_trigger_split_verifier: false`).

## Related Issues / Bugs
Part of #849

TAG=agy
CONV=38d3c465-a9ec-4546-9618-390a6bba10fe
