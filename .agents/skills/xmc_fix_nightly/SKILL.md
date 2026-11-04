---
name: XMC Fix Nightly
description: Investigates broken Nightly Examples E2E runs on main, performs root-cause triage across code regressions vs nightly cluster drift, implements minimal fixes in an isolated git worktree, opens a Draft PR, and proves the fix by triggering the nightly workflow on the PR branch.
---

# XMC Fix Nightly Playbook

This skill guides an agent end-to-end through discovering broken Nightly Examples E2E runs on `main`, performing root-cause triage, implementing a minimal fix in an isolated git worktree, opening a Draft PR, and proving the fix by running [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) against the PR branch.

**Scope**: `nightly-examples-e2e.yml` is the workflow this skill fixes and verifies. [`nightly-build.yml`](../../../.github/workflows/nightly-build.yml) runs and their `deploy-gke-diagnostics` artifacts are read-only triage inputs (Layer B), and dispatching `nightly-build.yml` requires explicit user confirmation (Stage 5). [`nightly-visual-snapshot.yml`](../../../.github/workflows/nightly-visual-snapshot.yml) is out of scope because it publishes the public GitHub Pages gallery.

> [!IMPORTANT]
> All execution steps MUST adhere to [Core Engineering Standards](../../rules/core_engineering.md), [Architectural Contracts & Invariants](../../rules/architectural_contracts.md), [Skills & Automation Guidelines](../../rules/skills_and_automation.md), and [GitHub Authoring & PR Creation Standards](../../rules/github_authoring_and_prs.md).

---

## Stage 1 — Discover & Triage Broken Nightly Examples E2E Run on `main`

Run [`tools/nightly_tests/triage_nightly_run.py`](../../../tools/nightly_tests/triage_nightly_run.py) to fetch `origin/main`, query recent `nightly-examples-e2e.yml` and `nightly-build.yml` runs plus open `nightly-failure` issues, download failed job logs (`job_<id>.log`) and non-expired failure artifacts (`result-<example_id>`, `ui-screenshots-*`, and `deploy-gke-diagnostics` when the active `nightly-build.yml` run failed) into `${ARTIFACT_DIR:-/tmp}/nightly_triage_<run_id>`, and compute the `<last_green_build_sha>..<failed_e2e_sha>` rollout-skew commit delta in one turn:

```bash
PYTHONPATH=. python3 tools/nightly_tests/triage_nightly_run.py
```

Pass `--run-id <run_id>` to triage a specific historical run. Delegate inspection of large downloaded `job_<id>.log` files or diagnostic bundles to a subagent per [Subagent Delegation Guidelines](../../rules/skills_and_automation.md#4-subagent-delegation-guidelines).

---

## Stage 2 — Root-Cause Triage & Investigation

Combine the JSON summary from Stage 1 (`failed_jobs`, `downloaded_artifacts`, `rollout_skew`) with recent `origin/main` commits (`git log origin/main --oneline -n 20`) and merged PRs (`gh pr list --repo google/xmc --state merged --limit 20`) to classify the failure layer:

### Layer A — Code, SDK, Example, Verifier, or Backend/UI Regression on `main`
- **Client / SDK / CLI Setup**: Packaging, proto-overlay, or import regressions in [`sdks/python/xmc/`](../../../sdks/python/xmc/), [`sdks/python/xdash/`](../../../sdks/python/xdash/), [`api/protos/`](../../../api/protos/), or [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py).
- **E2E Verifiers & Shared Dirty-Environment Scoping**: Stale selectors, DOM text assumptions, or truncated first-page assumptions in [`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/) or [`tools/nightly_tests/examples_verification/`](../../../tools/nightly_tests/examples_verification/) violating [Shared Dirty-Environment E2E & Pagination Testing](../../rules/core_engineering.md#5-strict-testing-coverage--pr-granularity) and [`tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md#step-1-implement-the-verifier-in-examples_verification).
- **Backend / UI / Manifest Defect**: Regressions in [`cmd/`](../../../cmd/), [`internal/`](../../../internal/), [`pkg/`](../../../pkg/), [`deploy/helm/`](../../../deploy/helm/), or [`deploy/ctk_v2/`](../../../deploy/ctk_v2/).

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
   - **Client / SDK / Example / Verifier / Workflow Changes**: `nightly-examples-e2e.yml` builds example images and runs the branch's SDKs and verifiers directly against `xmc-nightly`. Pass `--input "examples_filter=<id>"` using the exact `matrix.example.id` in [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) (validated pre-flight by [`dispatch_and_watch.py`](../../../tools/release/dispatch_and_watch.py)), or omit `--input` to run the full matrix when shared SDK/installer/workflow files changed.
   - **Cluster-Deployed Backend / UI / Helm / Addon Changes**: `nightly-examples-e2e.yml` tests against the server/UI currently deployed on `xmc-nightly`. Proving a server/UI change on `xmc-nightly` requires dispatching `nightly-build.yml` first (never `deploy-gke.yml` directly, whose `workflow_dispatch` defaults omit `xmc-nightly` addons). Because `nightly-build.yml` mutates shared `xmc-nightly` and pushes a `nightly-*` tag, **obtain explicit user confirmation before dispatching `nightly-build.yml` on a PR branch**.

2. **Dispatch & Watch `nightly-examples-e2e.yml` on the Pushed PR Branch**:
   Ensure the branch is pushed to `origin` first (Stage 4). [`tools/release/dispatch_and_watch.py`](../../../tools/release/dispatch_and_watch.py) validates `examples_filter` against `matrix.example.id`, fetches `origin/${BRANCH_NAME}` and resolves `FETCH_HEAD` automatically when `--target-sha` is omitted (per [Workflow Orchestration & Remote Automation Invariants](../../rules/skills_and_automation.md#9-workflow-orchestration--remote-automation-invariants)), correlates the newly created `workflow_dispatch` run, and watches it to completion (`conclusion == "success"`):
   ```bash
   PYTHONPATH=. python3 tools/release/dispatch_and_watch.py \
     --workflow nightly-examples-e2e.yml \
     --ref "fix/nightly-<kebab-case-slug>" \
     --input "examples_filter=<matrix_example_id>" \
     --state-file "${ARTIFACT_DIR:-/tmp}/nightly_verify_state.json"
   ```
   Omit `--input` (or pass `--input "examples_filter="`) when verifying the full matrix.

3. **Synchronize Draft PR Description & Report Proof**:
   Once PR CI (`./tools/pr_review/wait_for_ci.py <pr_number>` per [XMC Create PR — CI Wait Buffer Period](../xmc_create_pr/SKILL.md#5-ci-wait-buffer-period)) and the branch `nightly-examples-e2e.yml` run pass on `origin/${BRANCH_NAME}`, update the Draft PR description via `./tools/pr_review/update_pr_description.py` ([XMC Create PR — PR Description Updates via Helper Tool](../xmc_create_pr/SKILL.md#4-pr-description-updates-via-helper-tool)) with the passing verification run URL, create `.url.json` bookmark artifacts (`UserFacing: true`) for the Draft PR and verification run, and report back while keeping the PR in **Draft** state.
