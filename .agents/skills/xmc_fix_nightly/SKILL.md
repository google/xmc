---
name: XMC Fix Nightly
description: Investigates broken Nightly Examples E2E runs on main, performs root-cause triage across code regressions vs nightly cluster drift, implements minimal fixes in an isolated git worktree, opens a Draft PR, and proves the fix by triggering the nightly workflow on the PR branch.
---

# XMC Fix Nightly Playbook

This skill guides an agent end-to-end through discovering broken Nightly Examples E2E runs on `main`, performing root-cause triage, implementing a minimal fix in an isolated git worktree, opening a Draft PR, and proving the fix by running [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) against the PR branch.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to [Core Engineering Standards](../../rules/core_engineering.md), [Architectural Contracts & Invariants](../../rules/architectural_contracts.md), [Skills & Automation Guidelines](../../rules/skills_and_automation.md), and [GitHub Authoring & PR Creation Standards](../../rules/github_authoring_and_prs.md).

---

## Stage 1 — Discover Broken Nightly Examples E2E Run on `main`

1. **Fetch Latest `origin/main` First**:
   Active local checkouts may sit on older feature branches. Always fetch `origin/main` before inspecting workflows, code, or git history:
   ```bash
   git fetch origin main
   ```

2. **Query Recent Nightly Runs & Open Failure Issues**:
   Check recent runs of `nightly-examples-e2e.yml` (and `nightly-build.yml` when checking whether the preceding 01:00 UTC cluster deployment succeeded) along with open nightly-failure issues:
   ```bash
   gh run list --repo google/xmc --workflow=nightly-examples-e2e.yml --branch main --limit 10 \
     --json databaseId,status,conclusion,headSha,event,createdAt,url
   gh run list --repo google/xmc --workflow=nightly-build.yml --branch main --limit 5 \
     --json databaseId,status,conclusion,headSha,createdAt,url
   gh issue list --repo google/xmc --search "Nightly Examples E2E" --state open
   ```

3. **Extract Failed Jobs & Step Outcomes**:
   Inspect the latest failing (or user-specified) run on `main` to collect failed job IDs and steps:
   ```bash
   RUN_ID="<run_id>"
   gh run view "${RUN_ID}" --repo google/xmc --json status,conclusion,headSha,createdAt,url,jobs \
     --jq '{status, conclusion, headSha, createdAt, url, failed_jobs: [.jobs[] | select(.conclusion == "failure") | {id: .databaseId, name, url}]}'
   ```
   Inspect step-level timestamps and conclusions for each failed job:
   ```bash
   JOB_ID="<job_id>"
   gh api "repos/google/xmc/actions/jobs/${JOB_ID}" \
     --jq '.steps[] | "\(.number). \(.name): \(.conclusion) (\(.started_at) -> \(.completed_at))"'
   ```

4. **Download Job Logs & Failure Artifacts to Scratch Directory**:
   Save full job logs and artifacts under `${ARTIFACT_DIR:-/tmp}` rather than dumping raw logs into stdout (delegate large log analysis to a subagent per [Subagent Delegation Guidelines](../../rules/skills_and_automation.md#4-subagent-delegation-guidelines)):
   ```bash
   RUN_ID="<run_id>"
   JOB_ID="<job_id>"
   EXAMPLE_ID="<example_id>"
   SCRATCH_DIR="${ARTIFACT_DIR:-/tmp}/nightly_triage_${RUN_ID}"

   mkdir -p "${SCRATCH_DIR}"
   gh api "repos/google/xmc/actions/jobs/${JOB_ID}/logs" > "${SCRATCH_DIR}/job_${JOB_ID}.log"
   gh run download "${RUN_ID}" --repo google/xmc -n "result-${EXAMPLE_ID}" -D "${SCRATCH_DIR}/results"
   gh run download "${RUN_ID}" --repo google/xmc -n "ui-screenshots-${EXAMPLE_ID}" -D "${SCRATCH_DIR}/screenshots"
   ```
   *(Note: `ui-screenshots-<example_id>` exists only for matrix entries with `verify_ui: true`; for failed `nightly-build.yml` runs, download `deploy-gke-diagnostics` instead.)*

---

## Stage 2 — Root-Cause Triage & Investigation

Classify every failure into one of two layers by inspecting `origin/main` commits (`git log origin/main --oneline -n 20`) and recent merged PRs (`gh pr list --repo google/xmc --state merged --limit 20`) for established idioms before designing a fix:

### Layer A — Code, SDK, Example, Verifier, or UI/Backend Regression on `main`
- **Client / SDK / CLI Setup**: Packaging or import regressions in [`sdks/python/xmc/`](../../../sdks/python/xmc/), [`sdks/python/xdash/`](../../../sdks/python/xdash/), [`api/protos/`](../../../api/protos/), or [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py) (which installs `xmanager`, compiles local `.proto` overlays, removes wheel-bundled `xdash` directories that shadow PEP 660 editable installs, and installs local SDKs).
- **E2E Verifiers & Playwright Selectors**: Stale CSS selectors, DOM text assumptions, or race conditions in [`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/) or [`tools/nightly_tests/examples_verification/`](../../../tools/nightly_tests/examples_verification/) after UI or API updates (see [`tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md)).
- **Shared Dirty-Environment Assumption Violations**: Assuming `xmc-nightly` is empty or that newly created resources appear on an unfiltered first page (`pageSize`), violating [Shared Dirty-Environment E2E & Pagination Testing](../../rules/core_engineering.md#5-strict-testing-coverage--pr-granularity) and [Step 1 of `tools/nightly_tests/README.md`](../../../tools/nightly_tests/README.md#step-1-implement-the-verifier-in-examples_verification).
- **Backend / UI / Manifest Bug**: Defects in [`cmd/`](../../../cmd/), [`internal/`](../../../internal/), [`pkg/`](../../../pkg/), [`deploy/helm/`](../../../deploy/helm/), or [`deploy/ctk_v2/`](../../../deploy/ctk_v2/).

### Layer B — Nightly Cluster (`xmc-nightly`) Infrastructure or Rollout Skew
- **Client/Server Rollout Skew**: `nightly-build.yml` deploys `xmc-nightly` at 01:00 UTC while `nightly-examples-e2e.yml` runs at 04:00 UTC (or on demand). A commit merged after the last green `nightly-build.yml` run can introduce client expectations whose server counterpart is not yet deployed on `xmc-nightly`.
- **Cluster Addon / Deployment State Drift**: Missing pods, failed Helm hooks, or unapplied Gateway/HTTPRoute or ClickHouse/metrics addon state from an earlier failed `nightly-build.yml` run.

> [!CAUTION]
> **STRICT SHARED CLUSTER SAFETY INVARIANTS (`xmc-nightly`)**:
> 1. **Never Mutate `xmc-nightly` Directly**: NEVER manually modify, apply manifests to, delete resources in, or upgrade the shared `xmc-nightly` cluster unless explicitly instructed by the user.
> 2. **Never Overwrite Default `~/.kube/config`**: If read-only `kubectl` inspection of `xmc-nightly` is needed during triage, always isolate credentials to a scratch file (`export KUBECONFIG="${ARTIFACT_DIR:-/tmp}/kubeconfig_nightly"`) so the developer's personal cluster context is untouched.

---

## Stage 3 — Create an Isolated Worktree & Implement the Fix

1. **Create an Isolated Git Worktree from `origin/main`**:
   Never modify an active feature checkout. Create a clean worktree on a compliant `fix/` branch per [Git Branching & Naming Standards](../../rules/github_authoring_and_prs.md#1-git-branching--naming-standards):
   ```bash
   SLUG="<kebab-case-slug>"
   WORKTREE_DIR="/tmp/xmc-fix-nightly-${SLUG}"
   BRANCH_NAME="fix/nightly-${SLUG}"

   git fetch origin main
   git worktree add "${WORKTREE_DIR}" -b "${BRANCH_NAME}" origin/main
   ```

2. **Implement a Minimal, Single-Responsibility Fix**:
   Follow [XMC Feature Development](../xmc_feature_development/SKILL.md) and language-specific rules under [`.agents/rules/languages/`](../../rules/languages/):
   - **Minimalism & SRP**: Fix the exact root cause without bundling unrelated refactors, defensive dead code, or unrequested diagnostic hooks. Check recent merged PRs first so fixes reuse canonical helpers (such as [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py) or [`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/)).
   - **Test Cohesion Boundary**:
     - Production code ([`cmd/`](../../../cmd/), [`internal/`](../../../internal/), [`pkg/`](../../../pkg/)) and shared verification libraries ([`tools/nightly_tests/verification/`](../../../tools/nightly_tests/verification/), [`tools/nightly_tests/weekly_visual_snapshot.py`](../../../tools/nightly_tests/weekly_visual_snapshot.py)) **MUST** include high-fidelity unit tests (`*_test.go`, `*_test.ts`, `*_test.py`).
     - Standalone E2E verifier entrypoints in [`tools/nightly_tests/examples_verification/`](../../../tools/nightly_tests/examples_verification/) are pure E2E scripts verified against live environments—do **NOT** add brittle mock-heavy unit test files (`*_verify_test.py`) in `examples_verification/`.

3. **Run Pre-Commit Hooks & Affected Tests**:
   Diagnose any off-`PATH` tools (`node`, `ruff`, `buildifier`, `golangci-lint`) via [PATH-First Diagnosis](../../rules/core_engineering.md#9-diagnostic-protocol-bazel-ci-and-agent-shell-and-zero-task-polling), stage your changes, and run pre-commit and affected tests per [XMC Create PR — Section 2](../xmc_create_pr/SKILL.md#2-pre-commit-hooks--commit-creation) and [XMC Feature Development — Section 3](../xmc_feature_development/SKILL.md#3-test-execution-via-affected-tests-tool) until 100% green:
   ```bash
   git add -A
   ./.githooks/pre-commit
   ```

---

## Stage 4 — Open a Draft PR

Follow [XMC Create PR — Section 3](../xmc_create_pr/SKILL.md#3-pull-request-creation--pr-body-population) end-to-end:

1. **Commit & Push the Branch**:
   Commit with a Conventional Commit message (`fix(<scope>): <summary>`), technical root-cause rationale, `TAG=agy`, and `CONV=<conversation_id>`, then push to `origin`:
   ```bash
   BRANCH_NAME="fix/nightly-<slug>"
   git push -u origin "${BRANCH_NAME}"
   ```
   *(Ensure zero internal Buganizer IDs or private environment details appear in commits or PR descriptions per [Environment & URL Sanitization Policy](../../rules/github_authoring_and_prs.md#3-pull-request-creation--pr-sizing-standards). If an open GitHub nightly-failure issue exists, reference `Fixes #<issue_number>`.)*

2. **Diff Sizing Check & Mandatory User Confirmation Gate**:
   Run `./tools/analyze_diff_size.py --base-ref origin/main...`, prepare `pr_body.md` documenting the failing `main` run/job URL and root-cause analysis, and pause for the **Mandatory User Confirmation Gate** in [XMC Create PR — Section 3, Step 7](../xmc_create_pr/SKILL.md#3-pull-request-creation--pr-body-population) before running `gh pr create --draft`. Keep the PR strictly in **Draft** status ([Section 6](../xmc_create_pr/SKILL.md#6-draft-pr-to-open-conversion-policy-strict-confirmation-gate)).

---

## Stage 5 — Trigger Nightly Examples E2E on the PR Branch to Prove the Fix

1. **Choose the Verification Workflow Scope**:
   - **Client / SDK / Example / Verifier / Workflow Changes** (`examples/`, `sdks/`, `api/protos/`, `deploy/cli/`, `tools/nightly_tests/`, `.github/workflows/nightly-examples-e2e.yml`): `nightly-examples-e2e.yml` builds example images and executes the branch's SDKs and verifiers directly against `xmc-nightly`.
     - **Targeted filter**: Pass `-f examples_filter="<id>"` using the exact `matrix.example.id` from [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) (for example, `resource_dashboard_ui`—not the verifier filename `resource_observability_dashboard_ui_verify`, as an unmatched ID silently skips all matrix jobs).
     - **Full suite**: Omit `examples_filter` (or use `tools/release/dispatch_and_watch.py`) when shared SDK, CLI installer, or workflow plumbing changed.
   - **Cluster-Deployed Backend / UI / Helm / Addon Changes** (`cmd/`, `internal/`, `pkg/`, `deploy/`): `nightly-examples-e2e.yml` alone tests against whatever server/UI version is currently deployed on `xmc-nightly`. Proving a server/UI fix on `xmc-nightly` requires running `nightly-build.yml` on the branch first (never `deploy-gke.yml` directly, whose `workflow_dispatch` defaults omit `xmc-nightly` addons and inputs). Because `nightly-build.yml` mutates the shared `xmc-nightly` cluster and pushes a `nightly-*` tag, **ALWAYS obtain explicit user confirmation before dispatching `nightly-build.yml` on a PR branch** (or verify on the user's personal GKE dev environment instead).

2. **Dispatch & Monitor `nightly-examples-e2e.yml` on the PR Branch**:
   For targeted example verification:
   ```bash
   BRANCH_NAME="fix/nightly-<slug>"
   EXAMPLES_FILTER="<matrix_example_id>"

   gh workflow run nightly-examples-e2e.yml \
     --repo google/xmc \
     --ref "${BRANCH_NAME}" \
     -f examples_filter="${EXAMPLES_FILTER}"
   ```
   Retrieve the newly created run ID (matching your pushed branch `headSha`) and watch it to completion:
   ```bash
   BRANCH_NAME="fix/nightly-<slug>"
   gh run list --repo google/xmc --workflow=nightly-examples-e2e.yml --branch "${BRANCH_NAME}" --limit 5 \
     --json databaseId,status,conclusion,headSha,createdAt,url
   ```
   ```bash
   VERIFY_RUN_ID="<verify_run_id>"
   gh run watch "${VERIFY_RUN_ID}" --repo google/xmc --exit-status --interval 15
   ```
   *(When running the full unfiltered `nightly-examples-e2e.yml` suite, prefer `PYTHONPATH=. python3 tools/release/dispatch_and_watch.py --workflow nightly-examples-e2e.yml --ref "${BRANCH_NAME}" --target-sha "${TARGET_SHA}" --state-file "${ARTIFACT_DIR:-/tmp}/nightly_verify_state.json"` per [Workflow Orchestration Invariants](../../rules/skills_and_automation.md#9-workflow-orchestration--remote-automation-invariants).)*

3. **Synchronize Draft PR Description & Report Proof**:
   - Once both PR CI (`./tools/pr_review/wait_for_ci.py <pr_number>` per [XMC Create PR — Section 5](../xmc_create_pr/SKILL.md#5-ci-wait-buffer-period)) and the branch `nightly-examples-e2e.yml` run pass on the latest `HEAD` commit, update the Draft PR description (`## Verification & Testing` + title if scope evolved) via `./tools/pr_review/update_pr_description.py` per [XMC Create PR — Section 4](../xmc_create_pr/SKILL.md#4-pr-description-updates-via-helper-tool) so the PR description matches the final diff and embeds the passing verification run URL.
   - Create `.url.json` bookmark artifacts (`UserFacing: true`) for the Draft PR and passing verification workflow run, and report the root cause, fix summary, and verification proof to the user while leaving the PR in **Draft** state until explicitly instructed otherwise.
