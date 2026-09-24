---
name: XMC Release
description: Orchestrates an end-to-end XMC release workflow — deploying the latest main to nightly, running and verifying e2e tests, capturing visual snapshot galleries, generating and iterating on structured release notes with the user, and publishing the release.
---

# XMC Release Orchestration Playbook

This skill guides an agent through preparing, validating, and publishing an XMC release across 7 sequential phases.

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
gh workflow run nightly-build.yml --ref main
```

1. Retrieve the newly triggered workflow run ID (allow a short buffer for GitHub to register the run):
   ```bash
   sleep 5
   RUN_ID=$(gh run list --workflow=nightly-build.yml --limit 1 --json databaseId --jq '.[0].databaseId')
   echo "Triggered nightly build run ID: ${RUN_ID}"
   ```
2. Notify the user:
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
```

1. Retrieve the E2E workflow run ID (allow a short buffer for GitHub to register the run):
   ```bash
   sleep 5
   E2E_RUN_ID=$(gh run list --workflow=nightly-examples-e2e.yml --limit 1 --json databaseId --jq '.[0].databaseId')
   echo "Triggered E2E test suite run ID: ${E2E_RUN_ID}"
   ```
2. Notify the user:
   > *"Nightly deployment succeeded. Triggered nightly examples E2E test suite (`nightly-examples-e2e.yml`, Run ID: `<E2E_RUN_ID>`). Monitoring execution..."*
3. Watch the E2E run to completion:
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
```

1. Retrieve the visual snapshot run ID (allow a short buffer for GitHub to register the run):
   ```bash
   sleep 5
   VISUAL_RUN_ID=$(gh run list --workflow=nightly-visual-snapshot.yml --limit 1 --json databaseId --jq '.[0].databaseId')
   echo "Triggered visual snapshot run ID: ${VISUAL_RUN_ID}"
   ```
2. Monitor run completion:
   ```bash
   gh run watch "${VISUAL_RUN_ID}" --exit-status
   ```
3. Extract the deployed GitHub Pages URL or workflow summary from the run:
   ```bash
   gh run view "${VISUAL_RUN_ID}"
   ```
4. Provide the user with the snapshot gallery URL:
   > *"✅ Visual snapshot gallery generation completed."*
   > - **Run URL**: `https://github.com/google/xmc/actions/runs/<VISUAL_RUN_ID>`
   > - **Interactive Gallery Link**: `https://google.github.io/xmc/` (or the direct URL outputted by `deploy-pages`)

---

## Phase 5: Generate Release Notes via `xmc_generate_release_notes`

Execute the release metadata extraction tooling per [xmc_generate_release_notes](../xmc_generate_release_notes/SKILL.md):

1. **Extract Structured Release Metadata**:
   Determine the previous semantic version tag (excluding nightly tags) and extract metadata targeting `origin/main`:
   ```bash
   BASE_TAG=$(git describe --tags --abbrev=0 --match='[0-9]*.[0-9]*.[0-9]*' origin/main^ 2>/dev/null || git describe --tags --abbrev=0 --match='v[0-9]*.[0-9]*.[0-9]*' origin/main^)

   PYTHONPATH=. python3 tools/release/extract_release_metadata.py \
     --base-ref "${BASE_TAG}" \
     --target-ref origin/main \
     --output /tmp/release_metadata.json
   ```
2. **Read Metadata & Synthesize Notes**:
   Parse `/tmp/release_metadata.json` to inspect:
   - `recommended_version`: base version, recommended version, bump type (`major`, `minor`, `patch`), and technical rationale.
   - `flag_transitions`: feature flag flips or promotions to default `true`.
   - `gated_subsystems_status`: active vs preview features.
   - `raw_commits`: commits grouped by conventional commit types and PR numbers.
3. **Draft the 4-Section Release Notes File**:
   Draft the release notes at `/tmp/release_notes_v<version>.md` strictly adhering to the 4-section layout:
   - **Section 1: Public Changes (Available Out-of-the-Box)** (Promotions, new features, bug fixes).
   - **Section 2: Production-Ready Features Behind Feature Flags** (Approved preview features with activation instructions).
   - **Section 3: In-Development / Experimental Features Behind Flags** (Experimental toggles).
   - **Section 4: Operational, Infrastructure & Engineering Velocity** (CI, tooling, refactors).
4. **Present Proposal to User**:
   Present the proposed version increment, rationale, and full draft release notes in chat for review.

---

## Phase 6: Iterative Review with User

1. Pause execution and solicit user feedback on:
   - Version number recommendation.
   - Content and categorization of release notes.
   - Any missing highlights or manual additions.
2. If adjustments are requested, update `/tmp/release_notes_v<version>.md` accordingly using `replace_file_content` or `write_to_file`.
3. Repeat until the user explicitly confirms and approves the final version and content.

---

## Phase 7: Publish Release

> [!CAUTION]
> **Mandatory User Confirmation Gate**: Only proceed after the user explicitly types an affirmative confirmation (e.g., "approve release", "publish release").

1. Execute `publish_release.py` targeting `origin/main` with `--draft` to create the annotated git tag, push it to `origin`, and create a draft release on GitHub:
   ```bash
   PYTHONPATH=. python3 tools/release/publish_release.py \
     --target-commit origin/main \
     --tag <version> \
     --notes-file /tmp/release_notes_v<version>.md \
     --draft
   ```
   *(Note: `--draft` is used by default to provide a collaborative proofreading window on GitHub Releases before final public broadcast, unless the user explicitly requested immediate public publication).*
2. Confirm publication to the user:
   > *"🚀 **Release Published Successfully!**"*
   > - **Tag**: `<version>`
   > - **GitHub Release Link**: `https://github.com/google/xmc/releases/tag/<version>`
