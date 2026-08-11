## Summary
Replaces static unconditional test execution in GitHub Actions with a change-aware scope resolution engine and single-runner Bazel `rdeps` package test discovery. Eliminates redundant test suites on domain-isolated PRs (e.g. skips all backend Go tests and Spanner emulator startup when editing UI components or markdown docs) while ensuring 100% safety via Inverted Default Safety.

## Motivation & Context
Previously, `.github/workflows/unit-tests.yml` executed both `run-backend-tests` (which boots a live Cloud Spanner Emulator container and compiles ~100+ Go unit test packages) and `run-ui-tests` unconditionally on every push and PR. A previous attempt (`pr-244`) to partition test targets via `discover_test_matrix.py` was rolled back because a 9-way GitHub Actions matrix fan-out caused runner concurrency quota blowups, duplicate Spanner Emulator containers, and parallel Bazel cache thrashing.

This PR solves the excessive test execution issue without matrix fan-out by:
- Operating strictly within our existing **2-job runner cap** (`run-backend-tests` and `run-ui-tests`).
- Introducing `tools/resolve_test_scope.py` to classify diffs before heavy containers start.
- Applying **Inverted Default Safety**: Any file not explicitly inside `cmd/ui/**` or pure docs/deploy artifacts is treated as backend code, guaranteeing that newly created backend services/directories automatically trigger backend tests.
- Discovering affected backend unit tests via local single-runner Bazel `rdeps` queries.
- Evaluating job skipping gracefully in the aggregate status gate (`run-tests`) so skipped domain jobs report as **PASSED** to GitHub Branch Protection.

## Key Changes
- **New Test Scope Engine**: Added [`tools/resolve_test_scope.py`](file:///usr/local/google/home/vluk/.gemini/jetski/worktrees/xmc/optimize_test_dependency_execution/tools/resolve_test_scope.py) implementing inverted default safety, domain flags (`run_backend`, `run_ui`), and single-runner Bazel `rdeps` discovery.
- **Unit Test Suite**: Added [`tools/resolve_test_scope_test.py`](file:///usr/local/google/home/vluk/.gemini/jetski/worktrees/xmc/optimize_test_dependency_execution/tools/resolve_test_scope_test.py) verifying 10 boundary cases (UI-only, doc-only, markdown files anywhere, new unknown backend directories, `main` branch pushes, core `go.mod` updates).
- **Cleanup of Obsolete Matrix Scripts**: Removed `tools/discover_test_matrix.py`, `tools/discover_test_matrix_test.py`, and `tools/run_affected_tests.py`, and updated [`tools/BUILD.bazel`](file:///usr/local/google/home/vluk/.gemini/jetski/worktrees/xmc/optimize_test_dependency_execution/tools/BUILD.bazel).
- **Workflow Optimization**: Updated [`.github/workflows/unit-tests.yml`](file:///usr/local/google/home/vluk/.gemini/jetski/worktrees/xmc/optimize_test_dependency_execution/.github/workflows/unit-tests.yml) to add the light weight `resolve-scope` job, apply `if:` guards, invoke dynamic Bazel targets, and preserve branch check status.

## Verification & Testing
- **Commands Executed**:
  - `python3 ./tools/resolve_test_scope_test.py` -> **Passed 10/10 tests in 0.003s**
  - `./.githooks/pre-commit` -> **Passed all linters (gofmt, golangci-lint, addlicense, ruff, mypy)**
- **Coverage & Test Status**: All 10 scope unit tests pass cleanly. Verified that doc-only diffs, UI-only diffs, markdown files anywhere, and new unknown backend folder additions behave according to specification.

## Related Issues / Bugs
N/A

TAG=agy
CONV=6623d591-92f1-4cdd-8693-ab866572dd10
