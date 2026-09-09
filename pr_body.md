## Summary
Introduces a unified post-submit main pipeline (`.github/workflows/postsubmit-main.yml`) orchestrating backend Go unit tests, Angular UI tests, Helm chart configuration tests, and KinD/Playwright headless E2E tests in parallel on commits merged to `main`, with a terminal `needs` reporting job powered by `.github/scripts/postsubmit-main.js` that automatically posts failure breakdown comments and `@mentions` the author on the merged Pull Request if regressions occur.

## Motivation & Context
Currently, post-submit tests run as disconnected standalone workflows on `push: branches: [main]`. When regressions occur, developers must manually inspect the GitHub Actions tab to discover failures. This workflow consolidates post-submit validation into a single DAG execution and wires automated feedback directly to the PR that introduced the change, eliminating stale PR monitoring and accelerating regression remediation.

## Key Changes
- **New Workflow**: Added `.github/workflows/postsubmit-main.yml` with parallel jobs:
  - `run-backend-tests`: Go unit tests with Spanner emulator service.
  - `run-ui-tests`: Angular unit tests.
  - `run-chart-tests`: Helm chart configuration tests.
  - `run-e2e-tests`: KinD + Playwright headless E2E test harness.
  - `report-results`: `needs` aggregator running `if: always() && !cancelled()`.
- **Modular Reporting Script & Tests**:
  - Added `.github/scripts/postsubmit-main.js` with pure domain functions for suite result parsing, status filtering, and markdown report generation.
  - Added `.github/scripts/postsubmit-main.test.js` with 9 unit tests verifying job matrix evaluation, dedup marker logic, and comment generation.
- **Removed Duplicate Triggers**:
  - Removed redundant `on.push` triggers from `.github/workflows/unit-tests.yml`, `.github/workflows/e2e-tests.yml`, and `.github/workflows/lint.yml` to prevent duplicate CI runs on `main`.
- **Automated PR Failure Reporting**:
  - Automatically queries the merged PR associated with the commit SHA via GitHub REST API.
  - Generates a structured Markdown table summarizing test suite outcomes.
  - `@mentions` the PR author with direct links to the failing CI run.
  - Implements an invisible deduplication marker (`<!-- xmc-postsubmit-main-failure: <SHA> -->`) to prevent duplicate comments on retries.

## Assumptions
None — requirements were fully specified.

## Verification & Testing
- **Pre-Commit Verification**: Buildifier, `gofmt`, `golangci-lint`, `addlicense`, and Go test determinism checker passed cleanly.
- **Unit Tests**: Full `.github/scripts/*.test.js` suite (130 tests) passed cleanly.
- **Diff Sizing Check**: Passed all size thresholds.

## Related Issues / Bugs
N/A

TAG=agy
CONV=0aa21958-9d35-432b-a742-a18cc1d7aa10
