---
name: XMC Release
description: Orchestrates an end-to-end XMC release workflow — deploying the latest main to nightly, running and verifying e2e tests, capturing visual snapshot galleries, generating and iterating on structured release notes with the user, and publishing the release.
---

# XMC Release Orchestration Playbook

This skill guides an agent through preparing, validating, and publishing an XMC release across 5 sequential phases.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [Core Engineering Standards](../../rules/core_engineering.md) and [Skills & Automation Guidelines](../../rules/skills_and_automation.md).

### Resumption & Phase Selection
- **Idempotency & Recent Run Check**: If resuming an interrupted release or if `main` has already been deployed and tested today, check for existing successful workflow runs targeting the current `origin/main` commit before re-triggering:
  ```bash
  gh run list --workflow=nightly-build.yml --branch=main --status=success --limit 1
  gh run list --workflow=nightly-examples-e2e.yml --branch=main --status=success --limit 1
  ```
- **Direct Entry**: If deployment and E2E tests have already succeeded for the target `origin/main` commit, the agent may proceed directly to Phase 4 or Phase 5.
- **Post-Review Resumption**: When resuming after the user approval gate, proceed with executing the publishing step in Phase 5 using the approved `/tmp/release_notes_v<version>.md` without re-running earlier phases.

> [!CAUTION]
> **STRICT INVARIANT: RELEASE CONFIRMATION GATES & SAFEGUARDS**
> 1. If any deployment or E2E test fails, **ABORT IMMEDIATELY AND NOTIFY THE USER**. Never proceed with subsequent steps or release generation on a broken build.
> 2. **NEVER** create a git tag or publish a GitHub release without explicit, affirmative confirmation from the user in the current conversation turn.

---

## Prerequisites & Tools

- Ensure `gh` CLI is installed and authenticated (`gh auth status`).
- Ensure working directory is the root of the XMC repository (can be executed from any branch or git worktree).
- Fetch the latest remote tracking refs and tags to ensure local tooling has the latest commits and tags from `main` without needing to check out or switch branches:
  ```bash
  git fetch origin main --tags
  ```
- Ensure Python dependencies and `PYTHONPATH=.` are configured when running helper tools.

---

## Phase 1: Deploy Newest Main to Nightly

Trigger the nightly build and deployment workflow using GitHub Actions:

```bash
# Capture target commit and trigger workflow
TARGET_SHA=$(git rev-parse origin/main)
gh workflow run nightly-build.yml --ref main

# Poll until the workflow run for TARGET_SHA is registered (up to 30s)
for i in {1..6}; do
  RUN_ID=$(gh run list --workflow=nightly-build.yml --branch=main --limit 5 --json databaseId,headSha --jq ".[] | select(.headSha == \"${TARGET_SHA}\") | .databaseId" | head -n 1)
  if [ -n "${RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-build.yml run on ${TARGET_SHA} to register." >&2
  exit 1
fi
echo "Triggered nightly build run ID: ${RUN_ID}"
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
  1. Inspect the failed job logs:
     ```bash
     gh run view "${RUN_ID}" --log-failed
     ```
  2. **ABORT THE RELEASE PIPELINE IMMEDIATELY**.
  3. Notify the user with exact failure details and the run link:
     > *"🚨 **Release Aborted**: Nightly deployment failed on `main`."*
     > - **Run URL**: `https://github.com/google/xmc/actions/runs/<RUN_ID>`
     > - **Failure Summary**: `<Brief snippet of error from logs>`
  4. Stop execution and await user instructions.
- **If successful**: Proceed to Phase 3.

---

## Phase 3: Run End-to-End Test Suite & Abort on Failure

Trigger the nightly examples E2E test suite against the freshly deployed nightly environment:

```bash
gh workflow run nightly-examples-e2e.yml --ref main

# Poll until the E2E workflow run for TARGET_SHA is registered (up to 30s)
for i in {1..6}; do
  E2E_RUN_ID=$(gh run list --workflow=nightly-examples-e2e.yml --branch=main --limit 5 --json databaseId,headSha --jq ".[] | select(.headSha == \"${TARGET_SHA}\") | .databaseId" | head -n 1)
  if [ -n "${E2E_RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${E2E_RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-examples-e2e.yml run on ${TARGET_SHA} to register." >&2
  exit 1
fi
echo "Triggered E2E test suite run ID: ${E2E_RUN_ID}"
```

1. Notify the user:
   > *"Nightly deployment succeeded. Triggered nightly examples E2E test suite (`nightly-examples-e2e.yml`, Run ID: `<E2E_RUN_ID>`). Monitoring execution..."*
2. Watch the E2E run to completion:
   ```bash
   gh run watch "${E2E_RUN_ID}" --exit-status
   ```

### Outcome Handling:
- **If E2E tests fail**:
  1. Inspect failure logs:
     ```bash
     gh run view "${E2E_RUN_ID}" --log-failed
     ```
  2. **ABORT THE RELEASE PIPELINE IMMEDIATELY**.
  3. Notify the user:
     > *"🚨 **Release Aborted**: Nightly E2E test suite failed."*
     > - **Run URL**: `https://github.com/google/xmc/actions/runs/<E2E_RUN_ID>`
     > - **Failed Examples**: `<List of failed test matrix items>`
  4. Stop execution and await user instructions.
- **If all E2E tests pass**: Proceed to Phase 4.

---

## Phase 4: Generate Visual Snapshot Gallery

Generate the visual snapshot gallery verifying golden UI routes on the nightly deployment:

```bash
gh workflow run nightly-visual-snapshot.yml --ref main

# Poll until the visual snapshot workflow run for TARGET_SHA is registered (up to 30s)
for i in {1..6}; do
  VISUAL_RUN_ID=$(gh run list --workflow=nightly-visual-snapshot.yml --branch=main --limit 5 --json databaseId,headSha --jq ".[] | select(.headSha == \"${TARGET_SHA}\") | .databaseId" | head -n 1)
  if [ -n "${VISUAL_RUN_ID}" ]; then break; fi
  sleep 5
done

if [ -z "${VISUAL_RUN_ID}" ]; then
  echo "Error: Timed out waiting for nightly-visual-snapshot.yml run on ${TARGET_SHA} to register." >&2
  exit 1
fi
echo "Triggered visual snapshot run ID: ${VISUAL_RUN_ID}"
```

1. Monitor run completion:
   ```bash
   gh run watch "${VISUAL_RUN_ID}" --exit-status
   ```

### Outcome Handling:
- **If visual snapshot run fails**:
  1. Inspect the failure logs:
     ```bash
     gh run view "${VISUAL_RUN_ID}" --log-failed
     ```
  2. Notify the user with the failure details and ask whether to abort or proceed with release notes:
     > *"⚠️ **Visual Snapshot Gallery Failed**: Review logs at `https://github.com/google/xmc/actions/runs/<VISUAL_RUN_ID>`. Awaiting confirmation on whether to abort or proceed."*
- **If successful**: Proceed to step 2.

2. Extract the deployed GitHub Pages URL or workflow summary from the run:
   ```bash
   gh run view "${VISUAL_RUN_ID}"
   ```
3. Provide the user with the snapshot gallery URL:
   > *"✅ Visual snapshot gallery generation completed."*
   > - **Run URL**: `https://github.com/google/xmc/actions/runs/<VISUAL_RUN_ID>`
   > - **Interactive Gallery Link**: `https://google.github.io/xmc/` (or the direct URL outputted by `deploy-pages`)

## Phase 5: Generate Notes, Review, and Publish Release

Delegate the end-to-end release documentation, interactive user review, and publishing workflow to [xmc_generate_release_notes](../xmc_generate_release_notes/SKILL.md):

1. **Extract Metadata & Synthesize Notes**: Follow Section 1–4 of `xmc_generate_release_notes` to extract commit metadata targeting `origin/main` and draft `/tmp/release_notes_v<version>.md`.
2. **Interactive Review**: Follow Section 5 of `xmc_generate_release_notes` to present the proposed version increment and draft notes to the user, refining content iteratively based on feedback.
3. **Publishing Confirmation Gate**: Follow Section 6 of `xmc_generate_release_notes` to enforce the mandatory confirmation gate before executing `publish_release.py` targeting `origin/main` with `--draft`.
4. **Draft Release Promotion (Optional Follow-up)**: Once the user proofreads the draft release on GitHub and explicitly approves promotion to public:
   ```bash
   gh release edit <version> --draft=false
   ```

