---
name: XMC Fix Nightly
description: Investigates broken Nightly Examples E2E runs on main, performs root-cause triage across code regressions vs nightly cluster drift, implements minimal fixes in an isolated git worktree, opens a Draft PR, and proves the fix by triggering the nightly workflow on the PR branch.
---

# XMC Fix Nightly Playbook

**Scope**: [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) is the workflow this skill fixes and verifies on a PR branch. [`nightly-build.yml`](../../../.github/workflows/nightly-build.yml) runs and their `deploy-gke-diagnostics` artifacts are read-only triage inputs (Layer B), and dispatching `nightly-build.yml` requires explicit user confirmation (Stage 5). [`nightly-visual-snapshot.yml`](../../../.github/workflows/nightly-visual-snapshot.yml) is out of scope because it publishes the public GitHub Pages gallery.

---

## Stage 1 — Discover & Triage Broken Nightly Examples E2E Run on `main`

Run [`tools/nightly_tests/triage_nightly_run.py`](../../../tools/nightly_tests/triage_nightly_run.py) (see [`tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md#5-nightly-failure-triage-cli-triage_nightly_runpy)) to fetch `origin/main`, inspect recent nightly runs and open `nightly-failure` issues, download failed job logs and non-expired failure artifacts into `${ARTIFACT_DIR:-/tmp}/nightly_triage_<run_id>`, parse `failed_jobs[].result` (the `result-<id>/<id>.json` object, including `id`, `status`, `launch_status`, `verify_status`, and `xid`) and bounded `failed_jobs[].error_excerpt` `##[error]` context lines, collect SHA-pinned `recent_commits`, and compute rollout skew in one turn:

```bash
PYTHONPATH=. python3 tools/nightly_tests/triage_nightly_run.py
```

Pass `--run-id <run_id>` to triage a specific historical run. **Stop Gate**: Stop and report to the user (along with any stale `open_issues`) if `status_summary` is not `"failing"` (`"healthy"`, `"superseded"`, `"in_progress"`, or `"no_completed_runs"`), unless the user explicitly requested triaging a `"superseded"` `--run-id`. Always review `warnings` (such as non-superseding `workflow_dispatch` green runs, equal-SHA Layer B/flake hints, recent-commit fallbacks, or missing build history) before trusting `recent_commits` or `rollout_skew` in Stage 2. When `failed_jobs[].result` and `failed_jobs[].error_excerpt` are insufficient to pinpoint the failure, delegate inspection of full `job_<id>.log` files or `deploy-gke-diagnostics` bundles to a subagent per [Subagent Delegation Guidelines](../../rules/skills_and_automation.md#4-subagent-delegation-guidelines).

---

## Stage 2 — Root-Cause Triage & Investigation

Inspect the JSON summary from Stage 1—`failed_jobs` (`result`, `error_excerpt`, `failed_steps`), `recent_commits` (pinned to `target_run.headSha`, with squash-merged `(#<pr>)` subjects), `downloaded_artifacts`, and `rollout_skew`—to classify the failure layer:

### Layer A — Code, SDK, Example, Verifier, or Backend/UI Regression on `main`
- **Client / SDK / CLI Setup**: Packaging, proto-overlay, or import regressions in [`sdks/python/xmc/`](../../../sdks/python/xmc/), [`sdks/python/xdash/`](../../../sdks/python/xdash/), [`api/protos/`](../../../api/protos/), or [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py).
- **E2E Verifiers & Shared Dirty-Environment Scoping**: Stale selectors, DOM text assumptions, or truncated first-page assumptions in [`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/) or [`tools/nightly_tests/examples_verification/`](../../../tools/nightly_tests/examples_verification/) violating [Shared Dirty-Environment E2E & Pagination Testing](../../rules/core_engineering.md#5-strict-testing-coverage--pr-granularity) and [`tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md#step-1-implement-the-verifier-in-examples_verification).
- **Backend / UI / Manifest Defect**: Regressions in [`cmd/`](../../../cmd/), [`internal/`](../../../internal/), [`pkg/`](../../../pkg/), [`deploy/helm/`](../../../deploy/helm/), [`deploy/ctk_v2/`](../../../deploy/ctk_v2/), [`deploy/addons/`](../../../deploy/addons/), or [`deploy/clickhouse/`](../../../deploy/clickhouse/).

### Layer B — Nightly Cluster (`xmc-nightly`) Rollout Skew or State Drift
- **Client/Server Rollout Skew**: `nightly-build.yml` deploys `xmc-nightly` at 01:00 UTC while `nightly-examples-e2e.yml` runs at 04:00 UTC (or on demand); inspect `rollout_skew.undeployed_commits` and `rollout_skew.cluster_deployed_files` from Stage 1 to see whether the failing example depends on undeployed server changes merged after `rollout_skew.last_green_build_run`.
- **Addon / Deployment Drift**: Missing pods, failed Helm hooks, or unapplied Gateway/ClickHouse/MultiKueue state surfaced by `rollout_skew.active_build_run` and `deploy-gke-diagnostics`.

> [!CAUTION]
> **STRICT SHARED CLUSTER SAFETY INVARIANTS (`xmc-nightly`)**:
> 1. **Never Mutate `xmc-nightly` Directly**: NEVER manually modify, apply manifests to, delete resources in, or upgrade `xmc-nightly` unless explicitly instructed by the user.
> 2. **Isolate `KUBECONFIG` During Read-Only Triage**: Never overwrite default `~/.kube/config`; pass `export KUBECONFIG="${ARTIFACT_DIR:-/tmp}/kubeconfig_nightly"` for any read-only cluster inspection.

---

## Stage 3 — Create an Isolated Worktree & Implement the Fix

1. **Create an Isolated Git Worktree from `origin/main`**:
   ```bash
   SLUG="<kebab-case-slug>"
   WORKTREE_DIR="/tmp/xmc-fix-nightly-${SLUG}"
   BRANCH_NAME="fix/nightly-${SLUG}"

   git fetch origin main
   git worktree add "${WORKTREE_DIR}" -b "${BRANCH_NAME}" origin/main
   ```

2. **Implement & Verify a Minimal, Single-Responsibility Fix**:
   Follow [XMC Feature Development](../xmc_feature_development/SKILL.md) and language rules under [`.agents/rules/languages/`](../../rules/languages/):
   - Fix the root cause without bundling unrelated refactors or cleanups.
   - Add or update unit tests per [Strict Testing Coverage](../../rules/core_engineering.md#5-strict-testing-coverage--pr-granularity). Never delete or weaken existing tests or assertions to make a nightly run pass.
   - **Verifier fixes**: Keep `examples_verification/<name>_test.py` entrypoints thin and place any cluster- or browser-independent parsing, normalization, or decision logic in [`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/) with a paired `*_test.py` unit test per [Nightly E2E Verifier Entrypoints](../../rules/core_engineering.md#5-strict-testing-coverage--pr-granularity) and [`tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md#1-architecture--execution-lifecycle).
   - Stage explicit modified paths (never blind `git add -A`) and run `./.githooks/pre-commit` and affected Bazel tests until 100% green per [XMC Create PR — Pre-Commit Hooks & Commit Creation](../xmc_create_pr/SKILL.md#2-pre-commit-hooks--commit-creation) and [XMC Feature Development — Test Execution via Affected Tests Tool](../xmc_feature_development/SKILL.md#3-test-execution-via-affected-tests-tool).

---

## Stage 4 — Open a Draft PR

Delegate commit creation, diff sizing check, confirmation gate, and Draft PR creation to [XMC Create PR — Pull Request Creation & PR Body Population](../xmc_create_pr/SKILL.md#3-pull-request-creation--pr-body-population), adding these nightly-specific details to `pr_body.md`:
- Link the failing `main` workflow run/job URL and state the root-cause classification from Stage 2.
- Include `Fixes #<issue_number>` when an open `nightly-failure` GitHub Issue exists.
- Push `fix/nightly-${SLUG}` to `origin` and keep the PR strictly in **Draft** state ([XMC Create PR — Draft PR to Open Conversion Policy](../xmc_create_pr/SKILL.md#6-draft-pr-to-open-conversion-policy-strict-confirmation-gate)).

---

## Stage 5 — Trigger Nightly Examples E2E on the PR Branch to Prove the Fix

1. **Select Verification Workflow Scope**:
   - **Client / SDK / Example / Verifier / Workflow Changes**: `nightly-examples-e2e.yml` builds example images and runs the branch's SDKs and verifiers directly against `xmc-nightly`. Pass `--input "examples_filter=<ids>"` using the non-empty comma-separated `failed_jobs[].example_id` values from Stage 1 (validated pre-flight by [`dispatch_and_watch.py`](../../../tools/release/dispatch_and_watch.py)), or omit `--input` to run the full matrix when `example_id` is empty (non-matrix jobs such as `examples-import-check` or `build-example-images`) or when shared SDK/installer/workflow files changed.
   - **Cluster-Deployed Backend / UI / Helm / Addon Changes**: `nightly-examples-e2e.yml` tests against the server/UI currently deployed on `xmc-nightly`. Proving a server/UI change on `xmc-nightly` requires dispatching `nightly-build.yml` first (never `deploy-gke.yml` directly, whose `workflow_dispatch` defaults omit `xmc-nightly` addons). Because `nightly-build.yml` mutates shared `xmc-nightly` and pushes a `nightly-*` tag, **obtain explicit user confirmation first**, then run (and verify `conclusion == "success"`) before Stage 5.2:
     ```bash
     SLUG="${SLUG:-<kebab-case-slug>}"
     BRANCH_NAME="${BRANCH_NAME:-fix/nightly-${SLUG}}"

     PYTHONPATH=. python3 tools/release/dispatch_and_watch.py --workflow nightly-build.yml --ref "${BRANCH_NAME}"
     ```

2. **Dispatch & Watch `nightly-examples-e2e.yml` on the Pushed PR Branch**:
   Ensure `origin/${BRANCH_NAME}` is pushed first (Stage 4). [`tools/release/dispatch_and_watch.py`](../../../tools/release/dispatch_and_watch.py) fetches `origin/${BRANCH_NAME}` and resolves `FETCH_HEAD` automatically when `--target-sha` is omitted (per [Workflow Orchestration & Remote Automation Invariants](../../rules/skills_and_automation.md#9-workflow-orchestration--remote-automation-invariants)), correlates the newly created `workflow_dispatch` run, and watches it to completion (`conclusion == "success"`):
   ```bash
   SLUG="${SLUG:-<kebab-case-slug>}"
   BRANCH_NAME="${BRANCH_NAME:-fix/nightly-${SLUG}}"
   EXAMPLES_FILTER="<matrix_example_id_or_empty>"

   PYTHONPATH=. python3 tools/release/dispatch_and_watch.py \
     --workflow nightly-examples-e2e.yml \
     --ref "${BRANCH_NAME}" \
     --input "examples_filter=${EXAMPLES_FILTER}" \
     --state-file "${ARTIFACT_DIR:-/tmp}/nightly_verify_state.json"
   ```
   Set `EXAMPLES_FILTER=""` (or omit `--input`) when verifying the full matrix.

3. **Synchronize Draft PR Description & Report Proof**:
   Once PR CI (`./tools/pr_review/wait_for_ci.py <pr_number>` per [XMC Create PR — CI Wait Buffer Period](../xmc_create_pr/SKILL.md#5-ci-wait-buffer-period)) and the branch `nightly-examples-e2e.yml` run pass on `origin/${BRANCH_NAME}`, update the Draft PR description via `./tools/pr_review/update_pr_description.py` ([XMC Create PR — PR Description Updates via Helper Tool](../xmc_create_pr/SKILL.md#4-pr-description-updates-via-helper-tool)) with the passing verification run URL, create `.url.json` bookmark artifacts (`UserFacing: true`) for the Draft PR and verification run, and report back while keeping the PR in **Draft** state.
