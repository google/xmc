---
name: XMC Release
description: Orchestrates an end-to-end XMC release workflow — deploying the latest main to nightly, running and verifying e2e tests, capturing visual snapshot galleries, generating and iterating on structured release notes with the user, and publishing the release. Use when preparing, orchestrating, or publishing a formal XMC release from main.
---

# XMC Release Orchestration Playbook

This skill guides an agent through preparing, validating, and publishing an XMC release across 5 sequential phases.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [Core Engineering Standards](../../rules/core_engineering.md) and [Skills & Automation Guidelines](../../rules/skills_and_automation.md).

### Resumption & Phase Selection
- **Idempotency & Recent Run Check**: If resuming an interrupted release or if `main` has already been deployed and tested today, check for existing successful workflow runs targeting the current `origin/main` commit before re-triggering. Ensure `headSha` strictly matches `${TARGET_SHA}`:
  ```bash
  gh run list --workflow=nightly-build.yml --branch=main --status=success --limit 5 --json databaseId,headSha --jq ".[] | select(.headSha == \"${TARGET_SHA}\") | .databaseId"
  gh run list --workflow=nightly-examples-e2e.yml --branch=main --status=success --limit 5 --json databaseId,headSha --jq ".[] | select(.headSha == \"${TARGET_SHA}\") | .databaseId"
  ```
- **Direct Entry**: If deployment and E2E tests have already succeeded for the target `TARGET_SHA` commit, the agent may proceed directly to Phase 4 or Phase 5.
- **Post-Review Resumption**: When resuming after the user approval gate, proceed with executing the publishing step in Phase 5 using the release notes at `<appDataDir>/brain/<conversation-id>/release_notes_v<version>.md` (or `/tmp/release_notes_v<version>.md`) without re-running earlier phases.

> [!CAUTION]
> **STRICT INVARIANT: RELEASE CONFIRMATION GATES & SAFEGUARDS**
> 1. If any deployment or E2E test fails, **ABORT IMMEDIATELY AND NOTIFY THE USER**. Never proceed with subsequent steps or release generation on a broken build.
> 2. **NEVER** create a git tag or publish a GitHub release without explicit, affirmative confirmation from the user in the current conversation turn (see [Release Publishing Gate](../xmc_generate_release_notes/SKILL.md#6-mandatory-confirmation-gate--release-publishing)).

---

## Prerequisites & Tools

- Ensure `gh` CLI is installed and authenticated (`gh auth status`).
- Ensure working directory is the root of the XMC repository (can be executed from any branch or git worktree).
- Fetch the latest remote tracking refs and tags to ensure local tooling has the latest commits and tags from `main` without needing to check out or switch branches:
  ```bash
  git fetch origin main --tags
  ```
- **Initialize & Persist Release Target SHA**:
  Resolve the target commit from `origin/main` and initialize state tracking (persisted to `<appDataDir>/brain/<conversation-id>/release_state.json` or `/tmp/release_state.json`):
  ```bash
  TARGET_SHA=$(git rev-parse origin/main)
  echo "{\"target_sha\": \"${TARGET_SHA}\"}" > /tmp/release_state.json
  ```
- Ensure Python dependencies and `PYTHONPATH=.` are configured when running helper tools.

---

## Phase 1: Deploy Newest Main to Nightly

Trigger the nightly build and deployment workflow using GitHub Actions:

```bash
# Capture dispatch timestamp and trigger workflow
DISPATCH_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
gh workflow run nightly-build.yml --ref main

# Poll until the workflow_dispatch run created at or after DISPATCH_TIME is registered (up to 30s)
for i in {1..6}; do
  RUN_ID=$(gh run list --workflow=nightly-build.yml --branch=main --limit 5 --json databaseId,headSha,event,createdAt --jq ".[] | select(.event == \"workflow_dispatch\" and .createdAt >= \"${DISPATCH_TIME}\") | .databaseId" | head -n 1)
  if [ -n "${RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-build.yml dispatch run to register." >&2
  exit 1
fi

# Verify run commit strictly matches TARGET_SHA
RUN_SHA=$(gh run view "${RUN_ID}" --json headSha --jq '.headSha')
if [ "${RUN_SHA}" != "${TARGET_SHA}" ]; then
  echo "Error: Run headSha (${RUN_SHA}) does not match expected TARGET_SHA (${TARGET_SHA}). Aborting." >&2
  exit 1
fi

echo "Triggered nightly build run ID: ${RUN_ID} (Commit: ${RUN_SHA})"
```

Notify the user:
> *"Triggered nightly deployment workflow (`nightly-build.yml`) for `main` (Run ID: `<RUN_ID>`). Monitoring progress..."*

---

## Phase 2: Wait for Deployment & Abort on Failure

Monitor the deployment run until completion using `gh run watch`:

```bash
gh run watch "${RUN_ID}" --exit-status
```

### Outcome Handling:
- **If `gh run watch` exits with non-zero (FAILED / CANCELLED)**:
  1. Inspect the run conclusion and failed job logs:
     ```bash
     CONCLUSION=$(gh run view "${RUN_ID}" --json conclusion --jq '.conclusion')
     gh run view "${RUN_ID}" --log-failed | tail -n 40
     ```
  2. **If Cancelled (`CONCLUSION == "cancelled"`)**:
     A newer run in the `xmc-nightly-orchestration` concurrency group (or scheduled cron) may have superseded this execution. Notify the user:
     > *"⚠️ **Nightly Deployment Cancelled**: Run `<RUN_ID>` was cancelled (likely superseded by a newer run in the concurrency group). Ask whether to re-dispatch or resume tracking the superseding run."*
  3. **If Failed (`CONCLUSION == "failure"`)**:
     **ABORT THE RELEASE PIPELINE IMMEDIATELY**.
     Notify the user with exact failure details and the run link (avoiding raw log dumps):
     > *"🚨 **Release Aborted**: Nightly deployment failed on `main`."*
     > - **Run URL**: `https://github.com/google/xmc/actions/runs/<RUN_ID>`
     > - **Failure Summary**: `<Concise summary of error from failed jobs>`
  4. Stop execution and await user instructions.
- **If successful**:
  Extract and record the deployed nightly tag from the build outputs to ensure E2E tests target this exact build:
  ```bash
  DEPLOYED_TAG=$(gh run view "${RUN_ID}" --json jobs --jq '.jobs[] | select(.name == "Generate Nightly Context") | .steps[]? | select(.name == "Compute Nightly Tag") | .conclusion')
  ```
  Proceed to Phase 3.

---

## Phase 3: Run End-to-End Test Suite & Abort on Failure

Trigger the nightly examples E2E test suite against the freshly deployed nightly environment:

```bash
# Capture dispatch timestamp and trigger workflow
E2E_DISPATCH_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
gh workflow run nightly-examples-e2e.yml --ref main

# Poll until the workflow_dispatch run created at or after E2E_DISPATCH_TIME is registered (up to 30s)
for i in {1..6}; do
  E2E_RUN_ID=$(gh run list --workflow=nightly-examples-e2e.yml --branch=main --limit 5 --json databaseId,headSha,event,createdAt --jq ".[] | select(.event == \"workflow_dispatch\" and .createdAt >= \"${E2E_DISPATCH_TIME}\") | .databaseId" | head -n 1)
  if [ -n "${E2E_RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${E2E_RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-examples-e2e.yml dispatch run to register." >&2
  exit 1
fi

# Verify run commit strictly matches TARGET_SHA
E2E_RUN_SHA=$(gh run view "${E2E_RUN_ID}" --json headSha --jq '.headSha')
if [ "${E2E_RUN_SHA}" != "${TARGET_SHA}" ]; then
  echo "Error: E2E run headSha (${E2E_RUN_SHA}) does not match expected TARGET_SHA (${TARGET_SHA}). Aborting." >&2
  exit 1
fi

echo "Triggered E2E test suite run ID: ${E2E_RUN_ID} (Commit: ${E2E_RUN_SHA})"
```

1. Notify the user:
   > *"Nightly deployment succeeded. Triggered nightly examples E2E test suite (`nightly-examples-e2e.yml`, Run ID: `<E2E_RUN_ID>`). Monitoring execution..."*
2. Watch the E2E run to completion:
   ```bash
   gh run watch "${E2E_RUN_ID}" --exit-status
   ```

### Outcome Handling:
- **If E2E tests fail or are cancelled**:
  1. Inspect the run conclusion and summarize failed matrix jobs:
     ```bash
     E2E_CONCLUSION=$(gh run view "${E2E_RUN_ID}" --json conclusion --jq '.conclusion')
     gh run view "${E2E_RUN_ID}" --json jobs --jq '.jobs[] | select(.conclusion == "failure") | {name: .name, conclusion: .conclusion}'
     ```
  2. **If Cancelled (`E2E_CONCLUSION == "cancelled"`)**:
     Notify the user that the E2E run was cancelled and inquire whether to re-dispatch.
  3. **If Failed (`E2E_CONCLUSION == "failure"`)**:
     - Check if failure appears to be a known transient infrastructure flake (e.g., runner network timeout).
     - The agent may offer the user a single approved retry:
       ```bash
       gh run rerun "${E2E_RUN_ID}" --failed
       ```
     - If the failure persists or retry is not approved, **ABORT THE RELEASE PIPELINE IMMEDIATELY**.
     - Notify the user with a concise summary (avoiding raw log dumps):
       > *"🚨 **Release Aborted**: Nightly E2E test suite failed."*
       > - **Run URL**: `https://github.com/google/xmc/actions/runs/<E2E_RUN_ID>`
       > - **Failed Workloads**: `<List of failed example jobs>`
  4. Stop execution and await user instructions.
- **If all E2E tests pass**: Proceed to Phase 4.

---

## Phase 4: Generate Visual Snapshot Gallery

Generate the visual snapshot gallery verifying golden UI routes on the nightly deployment:

```bash
# Capture dispatch timestamp and trigger workflow
VISUAL_DISPATCH_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
gh workflow run nightly-visual-snapshot.yml --ref main

# Poll until the workflow_dispatch run created at or after VISUAL_DISPATCH_TIME is registered (up to 30s)
for i in {1..6}; do
  VISUAL_RUN_ID=$(gh run list --workflow=nightly-visual-snapshot.yml --branch=main --limit 5 --json databaseId,headSha,event,createdAt --jq ".[] | select(.event == \"workflow_dispatch\" and .createdAt >= \"${VISUAL_DISPATCH_TIME}\") | .databaseId" | head -n 1)
  if [ -n "${VISUAL_RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${VISUAL_RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-visual-snapshot.yml dispatch run to register." >&2
  exit 1
fi

# Verify run commit strictly matches TARGET_SHA
VISUAL_RUN_SHA=$(gh run view "${VISUAL_RUN_ID}" --json headSha --jq '.headSha')
if [ "${VISUAL_RUN_SHA}" != "${TARGET_SHA}" ]; then
  echo "Error: Visual snapshot run headSha (${VISUAL_RUN_SHA}) does not match expected TARGET_SHA (${TARGET_SHA}). Aborting." >&2
  exit 1
fi

echo "Triggered visual snapshot run ID: ${VISUAL_RUN_ID} (Commit: ${VISUAL_RUN_SHA})"
```

1. Monitor run completion:
   ```bash
   gh run watch "${VISUAL_RUN_ID}" --exit-status
   ```

### Outcome Handling & Gallery Verification:
- **Inspect Run and Pages Deployment Step**:
  The `Deploy to GitHub Pages` step uses `continue-on-error: true`, so `gh run watch` may exit 0 even if publishing the gallery failed. Inspect step outcomes:
  ```bash
  PAGE_DEPLOY_CONCLUSION=$(gh run view "${VISUAL_RUN_ID}" --json jobs --jq '.jobs[] | select(.name == "Deploy Gallery to GitHub Pages") | .conclusion // "failure"')
  ```
- **If visual snapshot run fails or pages deploy failed**:
  1. Inspect the failed job summary:
     ```bash
     gh run view "${VISUAL_RUN_ID}" --log-failed | tail -n 40
     ```
  2. Notify the user with the failure details and ask whether to abort or proceed with release notes:
     > *"⚠️ **Visual Snapshot Gallery Incomplete/Failed**: Review logs at `https://github.com/google/xmc/actions/runs/<VISUAL_RUN_ID>`. Pages deploy status: `<PAGE_DEPLOY_CONCLUSION>`. Awaiting confirmation on whether to abort or proceed to Phase 5."*
  3. If user decides to abort, stop pipeline. If user confirms proceeding despite snapshot failure, proceed to Phase 5.
- **If successful**:
  Extract the deployed GitHub Pages URL from the step summary:
  ```bash
  GALLERY_URL=$(gh run view "${VISUAL_RUN_ID}" | grep -o 'https://[^ ]*github\.io/xmc/' | head -n 1)
  ```
  Provide the user with the verified snapshot gallery URL:
  > *"✅ **Visual snapshot gallery generation completed**."*
  > - **Run URL**: `https://github.com/google/xmc/actions/runs/<VISUAL_RUN_ID>`
  > - **Interactive Gallery Link**: `${GALLERY_URL:-https://google.github.io/xmc/}`

---

## Phase 5: Generate Notes, Review, and Publish Release

Delegate the end-to-end release documentation, interactive user review, and publishing workflow to [xmc_generate_release_notes](../xmc_generate_release_notes/SKILL.md):

1. **Extract Metadata & Synthesize Notes**:
   Follow [Release Range & Discovery](../xmc_generate_release_notes/SKILL.md#1-release-range--git-reference-discovery) and [Deterministic Metadata Extraction](../xmc_generate_release_notes/SKILL.md#2-deterministic-metadata-extraction--approved-flags-discovery).
   Ensure `--target-ref "${TARGET_SHA}"` is explicitly passed to `extract_release_metadata.py` using the persisted `${TARGET_SHA}` from Prerequisites:
   ```bash
   python3 ./tools/release/extract_release_metadata.py \
       --base-ref <base_tag> \
       --target-ref "${TARGET_SHA}" \
       --approved-flags rod_deployment_enabled rodDeployment.enabled enable_metrics metrics.enabled \
       --output /tmp/release_metadata.json
   ```
   Follow [Semantic Commit Classification](../xmc_generate_release_notes/SKILL.md#3-semantic-commit-classification--section-routing-agent-reasoning) and [Semantic Synthesis & Narrative Formatting](../xmc_generate_release_notes/SKILL.md#4-semantic-synthesis--narrative-formatting) to draft the release notes at `/tmp/release_notes_v<version>.md` (or `<appDataDir>/brain/<conversation-id>/release_notes_v<version>.md`).

2. **Interactive Review**:
   Follow [Draft Generation & Interactive Review Gate](../xmc_generate_release_notes/SKILL.md#5-draft-generation--interactive-review-gate) to present the proposed version increment and draft notes to the user, refining content iteratively based on feedback.

3. **Preflight Validation & Publishing Confirmation Gate**:
   Before asking for user confirmation to publish, perform a non-mutating preflight dry-run targeting `${TARGET_SHA}`:
   ```bash
   PYTHONPATH=. python3 ./tools/release/publish_release.py --tag <version> --notes-file <path_to_markdown_file> --target-commit "${TARGET_SHA}" --dry-run
   ```
   Follow [Mandatory Confirmation Gate & Release Publishing](../xmc_generate_release_notes/SKILL.md#6-mandatory-confirmation-gate--release-publishing) to enforce the mandatory affirmative confirmation gate.
   Once approved by the user in chat, execute publication targeting `${TARGET_SHA}` with `--draft`:
   ```bash
   PYTHONPATH=. python3 ./tools/release/publish_release.py --tag <version> --notes-file <path_to_markdown_file> --target-commit "${TARGET_SHA}" --draft
   ```
   *(Note: Never pass `--force`. If the tag already exists, abort and consult the user).*


