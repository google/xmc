# GitHub Authoring & PR Creation Standards

This document defines standards and policy rules for AI agents acting as **Code Authors / Contributors** (writing code, managing branches, generating commits, creating pull requests, and addressing review comments).

> [!IMPORTANT]
> Operational step-by-step playbooks for branch checking, executing pre-commit hooks, formatting PR bodies, updating PR descriptions, and managing CI wait buffers are defined in the [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md).

---

## 1. Git Branching & Naming Standards
- **Branch Naming Schema**: Name branches using lowercase kebab-case in the format `<prefix>/<short-descriptive-name>` (or `<prefix>/<issue-id>-<short-description>`).
- **Standard Branch Prefixes**:
  - `feat/`: New feature implementation or structural additions (e.g., `feat/enforce-pr-comment-integrity`).
  - `fix/`: Bug fixes and defect resolutions (e.g., `fix/gke-rollout-timeout`).
  - `refactor/`: Code restructure or cleanups without behavior or public API changes (e.g., `refactor/pep8-compliance`).
  - `docs/`: Documentation updates (e.g., `docs/ctk-installation-guide`).
  - `test/`: Adding or refactoring unit, integration, or E2E tests.
  - `chore/`: Maintenance, build script edits, Bazel updates, or dependency bumps (e.g., `chore/bump-kueue-version`).
  - `koder/`: Automated pull requests and branches produced by the autocoder agent (koder).
- **Session Initialization Branch Check Policy**: Immediately upon starting a task, agents MUST verify that the current branch strictly conforms to the `<prefix>/<kebab-case-description>` format. Non-compliant branch names (or detached HEAD states) MUST be renamed or checked out to a compliant branch before making code changes or rebasing. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#1-branch-check--formatting-verification) for procedure. In a linked worktree, run `git fetch origin`, then create new branches from `origin/main` (`git checkout -b <prefix>/<name> origin/main`). Before switching to an existing branch, run `git worktree list` to confirm it is not already checked out in another worktree.
- **Forbidden Branch Naming Anti-Patterns**:
  - NEVER use un-prefixed branch names (e.g., `enforce_pr_comment_integrity`).
  - NEVER rely on raw worktree directory names or task IDs without verifying `<prefix>/<kebab-case-description>` format.
  - NEVER use underscores (`_`) in branch names; always use hyphens (`-`) for word separators.

## 2. Git Commits & Formatting Standards
- **Conventional Commit Format**: Format commit subject lines using Conventional Commits syntax:
  `<type>(<scope>): <summary>` (e.g., `feat(auth): add OAuth2 token refresh support` or `fix(reconciler): resolve deadlock on pod restart`).
  - Allowed types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`.
  - Write subject lines in the imperative mood, using lowercase, and without trailing periods.
- **Mandatory Pre-Commit Formatting Standard**: All modified files MUST pass repository pre-commit hooks prior to committing. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#2-pre-commit-hooks--commit-creation) for the execution playbook.
- **Detailed Commit Body**: Provide context in the commit body describing *why* the changes were made, architectural choices, and technical rationale—not merely a list of modified files.
- **Traceability & Open-Source TODO Standards**:
  - When addressing review comments, include the comment link in the commit body: `Comment link: <github_comment_url>`.
  - When referencing bugs, include the bug ID in plain text (e.g., `Fixes bug <bug_id>`, `Closing bug <bug_id>`, or `Bug: <bug_id>`).
  - **Open-Source TODO Standard**: Follow the [Open-Source TODO Standard](core_engineering.md#5-strict-testing-coverage--pr-granularity) (prohibiting internal Buganizer IDs in public code `TODO`s).
- **Mandatory Description Tags**: All CL/PR descriptions MUST end with mandatory system tags placed at the very bottom after all human-readable text:
  ```markdown
  TAG=agy
  CONV=<conversation_id>
  ```
- **Atomic Commits**: Keep commits logical, focused, and self-contained. Each commit must compile and pass relevant local tests.

## 3. Pull Request Creation & PR Sizing Standards
- **Mandatory PR Sizing & Vertical Splitting Gate**:
  - **Threshold Criteria (Evaluated via `tools/analyze_diff_size.py`)**:
    - **Production Code**: $\ge 400$ lines (added + modified across `.go`, `.py`, `.ts`, `.js`, `.ng.html`, `.html`, `.scss`, `.css`, `.yaml`, `.tf`, `.proto`).
    - **Combined Code (Prod + Tests)**: $\ge 1,000$ lines (including unit, integration, and E2E tests).
    - **Documentation / Markdown**: $\ge 1,000$ lines (measured separately from code).
    - *Generated Code & Lockfiles*: Automatically excluded (`*.pb.go`, `package-lock.json`, `MODULE.bazel.lock`, `go.sum`).
  - **Mandatory Split Verification Subagent**: If any sizing threshold is exceeded, the author agent MUST launch the **PR Split Verification Subagent** per [XMC PR Split Verification Skill](../skills/xmc_pr_split_verification/SKILL.md) prior to opening a PR.
  - **Vertical Slices over Horizontal Layers**: Always decompose work into vertical end-to-end user flow slices (schema + backend + handler + tests) rather than horizontal architectural tiers (all models in PR 1, all handlers in PR 2).
  - **Dead Code & Reachability Invariant**:
    - **Production Caller Required**: Every intermediate or standalone PR merged to `main` MUST have an active production call site. Submitting uncalled helpers, orphaned utilities, or unused structs (even with 100% unit tests in a planned `gh-stack`) is prohibited because stacks can stall or change, leaving dead code in `main`.
    - **Functional Thin Slices over Layer Splits**: Decompose oversized features by functional depth (e.g. Slice 1: minimal happy-path caller + basic helper; Slice 2: advanced options, caching, and error recovery) rather than by structural layer.
    - **Standalone Entrypoint Exception**: Self-contained executable entrypoints (e.g. scaffolding a new CLI binary in `cmd/`, a new service binary, or a standalone script in `tools/`) are permitted without external callers because they are directly runnable on their own.
  - **Stacked PR Workflow (`gh-stack`)**: When splittable, format and manage the multi-branch chain using `gh-stack`. Restack with `gh stack sync` or `gh stack rebase`, never raw `git rebase --onto` (it can silently drop commits); after a parent PR is squash-merged, run `git fetch origin`, restack with `gh stack rebase`, confirm `git log --oneline origin/main..<branch>` lists only this PR's own commits, then push with `gh stack push`. Commit or stash before any rebase. For non-stacked PRs synced by merge, after `git fetch origin`, give the merge commit a Conventional subject (`git merge origin/main -m "chore: merge origin/main"`).
  - **Exception Justification**: Monolithic PRs exceeding thresholds are permitted only for atomic automated migrations, large code deletions, or single-document design specs, and require explicit user confirmation.
- **Mandatory User Confirmation Gate Before PR Creation & Draft Conversion**: NEVER execute `gh pr create` (draft or non-draft) or `gh pr ready` without explicit, affirmative user confirmation in the current conversation turn. Phrases like "proceed with next phase" authorize local work and verification, but DO NOT authorize opening a remote Pull Request or converting a Draft PR to Open. Draft PRs MUST NEVER be converted to Open (ready for review) without a separate, explicit user confirmation gate. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#3-pull-request-creation--pr-body-population) (Step 7) and [Section 6](../skills/xmc_create_pr/SKILL.md#6-draft-pr-to-open-conversion-policy-strict-confirmation-gate) for mandatory confirmation templates.
- **Pre-Commit Verification**: Before committing or opening a PR, ensure all repository linter hooks pass (Bazel buildifier, Go formatter, addlicense, Ruff Python linter) and unit tests pass cleanly.
- **Conventional PR Titles**: Match the PR title to Conventional Commits syntax (`<type>(<scope>): <summary>`).
- **Mandatory PR Body Population**: ALWAYS populate the official PR Body/Description field upon creation with full context, summary, architectural intent, test verification results, and issue references.
- **Mandatory Assumptions Disclosure**: Every PR authored by an AI agent MUST document, in a dedicated `## Assumptions` section of the PR body, each assumption the agent made while implementing the change. The section is **always present**—when the requirements fully determined the implementation, state `None — requirements were fully specified.` rather than deleting the heading, so reviewers can distinguish "no assumptions" from "author forgot".
  - **What Counts as an Assumption**: Any decision the agent made in place of an explicit instruction or verified fact—inferred requirements and acceptance criteria, guessed input value ranges or units, presumed upstream/downstream behavior not read from source, chosen defaults for unspecified configuration, assumed backward-compatibility or migration expectations, environments and versions believed to be in use, and any scope the agent deliberately deferred.
  - **Disclosure Entry Format**: Record one bullet per assumption as `<assumption> — <why it was assumed> — <impact if wrong>`. The impact clause is mandatory: it tells the reviewer which assumptions are cheap to accept and which must be verified before merge.
  - **Verify Before Assuming**: Assumptions are a last resort, not a shortcut. If an assumption is cheaply checkable by reading the code, running a test, or asking the user, resolve it instead of listing it. Reserve the section for what genuinely could not be settled during the task.
  - **Halt on Load-Bearing Assumptions**: If proceeding under an unverified assumption would silently break a public API, protobuf schema, CLI flag, security boundary, or any core system contract, agents MUST NOT resolve it unilaterally—seek explicit user confirmation per **System Contract & API Changes** in [Core Engineering Standards](core_engineering.md#5-strict-testing-coverage--pr-granularity) before implementing.
- **Mandatory PR Description Update Tool Policy**: When updating an existing PR description, ALWAYS use `tools/pr_review/update_pr_description.py` via the procedure detailed in [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#4-pr-description-updates-via-helper-tool). Do NOT use `gh pr edit` or raw inline shell commands, as GraphQL classic projects deprecation breaks `gh pr edit`.
- **Prohibition of Raw Log Dumps vs. Allowed Agent Artifacts**:
  - **Forbidden Raw Noise**: NEVER copy-paste un-formatted `git log` output, raw commit history, or indiscriminate wall-of-text commit listings into the PR description or PR comments.
  - **Allowed Implementation Plans & Audit Artifacts**: Posting structured **Implementation Plans**, original prompt metadata, or review iteration discussions in PR comments (or collapsible `<details>` blocks) is explicitly allowed for autocoder (`koder`) tasks to enable post-mortem debugging and prompt evaluation.
- **Standard PR Body Template**: Every created PR description must strictly follow this markdown structure:
  ```markdown
  ## Summary
  <Concise 2-3 sentence overview of what this PR introduces or fixes.>

  ## Motivation & Context
  <Explanation of the underlying problem, business/technical rationale, and design choices.>

  ## Key Changes
  - <Bulleted highlight of structural code adjustments>
  - <Targeted package, binary, or deployment modifications>

  ## Assumptions
  - <Assumption made during development> — <why it was assumed> — <impact if wrong>
  - <Deferred scope or unresolved open question, if any>
  <Replace the bullets above with `None — requirements were fully specified.` when no assumptions were made.>

  ## Verification & Testing
  - **Commands Executed**: `<bazel test / go test / ./tools/run_affected_tests.py>`
  - **Coverage & Test Status**: <Summary of unit/integration test results and pass state.>

  ## Related Issues / Bugs
  Fixes bug <bug_id> (or N/A)

  TAG=agy
  CONV=<conversation_id>
  ```
- **Environment & URL Sanitization Policy**: All tracked public markdown (`**/*.md`, including `docs/**`, `.agents/**`, root `README.md`/`INSTALL.md`/`CONTRIBUTING.md`, and component READMEs), PR descriptions, and public commit messages must never expose specific development environment identifiers, private cluster names, internal domain hostnames (`*.example.com`), test GCP project IDs, or live internal cluster/runner capacity sizing numbers (e.g., exact GKE node pool `minNodeCount`/`maxNodeCount` or ARC `minRunners`/`maxRunners` replica counts). Use generalized phrasing (e.g., "Live GKE Development Environment via deployment script" or generic example placeholders) in public docs and manual verification evidence.
- **PR Granularity & Single Responsibility**: Limit each PR to a single, cohesive change. Avoid bundling unrelated code refactoring, whitespace reformatting, or dependency upgrades inside feature/bugfix PRs.

## 4. Post-PR Creation Workflow & Addressing Review Feedback Policy
- **Wait Buffer Policy After PR Creation**: When instructed to create or update a Pull Request, after creating the PR, agents MUST pause for approximately **2 minutes** to allow GitHub CI status checks and automated GitHub PR review passes to complete. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#5-ci-wait-buffer-period). After the buffer, wait for CI with `./tools/pr_review/wait_for_ci.py <pr_number>` instead of polling bare `gh pr checks`.
- **Strict Draft-to-Open PR Conversion Policy**: Pull Requests in XMC MUST be created as Draft PRs by default. Agents are strictly prohibited from converting a Draft PR to Open (`gh pr ready`) automatically. Even if all CI status checks pass and 100% of review comments are resolved, the PR MUST remain in Draft state until the user explicitly confirms converting it to Open.
- **Addressing Review Comments**: When review feedback is received, execute the detailed comment resolution workflow defined in the [Address PR Comment Skill](../skills/xmc_address_pr_comment/SKILL.md) (fetching comment context, generating checklist artifacts, running local tests, confirming contract changes with the user, posting inline thread replies, and verifying 1-to-1 resolution).
- **Prohibition of Force-Pushing on PR Branches**: NEVER use force-push (`git push --force` or `git push --force-with-lease`) when pushing fix commits or addressing review comments on an active Pull Request branch. Always append incremental commits to the branch so reviewers can easily inspect diffs before and after changes between review passes. **Exception**: restacking your own unmerged stack branches with `gh stack push` (or `git push --force-with-lease` on your own branch after a rebase) is allowed, because a rebase onto an updated parent replays the same commits. NEVER force-push shared branches or branches carrying others' commits, and NEVER rewrite reviewed commits (squash, amend, or drop them) without the user's approval.

## 5. Staging Hygiene & Incremental Work Preservation
- **Staging Hygiene**: Ensure temporary commit message drafts (`commit_*_msg.txt`), scratch files, or un-formatted logs are not staged or tracked prior to committing.
- **Incremental Work Checkpoints**: For multi-step tasks, push incremental WIP commits to a dedicated feature branch to prevent uncommitted work loss on session termination.

## 6. GitHub Actions Workflow & Composite Action Security (`zizmor` Compliance)
Whenever creating, modifying, or reviewing GitHub Actions workflow files under `.github/workflows/*.{yml,yaml}` or composite actions under `.github/actions/*/action.{yml,yaml}`:

1. **Pin Action References to Full Commit SHAs (`zizmor/unpinned-uses`)**:
   - NEVER use mutable version tags or branch names in `uses:` steps (e.g., `actions/checkout@v4`, `actions/github-script@v7`).
   - ALWAYS pin all remote/third-party action references to exact 40-character commit SHAs (excluding local path references like `uses: ./.github/actions/...`), accompanied by an inline comment displaying the human-readable version tag.
   - *Example (commit SHAs are illustrative and will vary depending on the action and target version):*
     ```yaml
     uses: 'actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683' # v4.2.2
     uses: 'actions/github-script@60a0d83039c74a4aee543508d2ffcb1c3799cdea' # v7.0.1
     ```

2. **Avoid Insecure Workflow Triggers (`zizmor/pull-request-target`)**:
   - Prefer standard `pull_request:` triggers over `pull_request_target:` for PR workflows.
   - `pull_request_target:` runs in the context of the base branch with write permissions and access to repository secrets, which `zizmor` flags as a security finding.
   - If `pull_request_target:` is strictly required (e.g., for workflows that require write permissions for PRs originating from forks), place an explicit `# zizmor: ignore[pull-request-target]` comment above the trigger block accompanied by an inline comment explaining *why* the trigger is necessary:
     ```yaml
     on:
       # zizmor: ignore[pull-request-target]
       # Required to grant PR write permissions for auto-approving ABSENCE.md updates on fork PRs.
       pull_request_target:
     ```

3. **Post-Checkout Git Credentials & Ref Availability (`persist-credentials: false`)**:
   - Because `actions/checkout` with `persist-credentials: false` strips the Git HTTP authorization header at the end of the `Checkout` step itself, subsequent workflow or composite action steps MUST NOT run unauthenticated `git fetch` or `git pull` commands (e.g., `git fetch origin ... || true`), which fail authentication on private repositories.
   - Instead, either rely on refs already fetched during checkout by specifying `fetch-depth: 0` on `actions/checkout` (which fetches `+refs/heads/*:refs/remotes/origin/*`), or explicitly pass `GITHUB_TOKEN` via an `http.extraheader` configuration when live remote ref fetching is required.
   - **Scope the auth header per invocation only**: Pass it via `git -c "http.extraheader=AUTHORIZATION: basic <base64(x-access-token:${GITHUB_TOKEN})>" fetch ...` or via `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0` environment variables. NEVER persist it with `git config http.extraheader ...`, which writes the token into `.git/config` for the rest of the job and re-introduces the credential persistence (`zizmor/artipacked`) that `persist-credentials: false` prevents. The header MUST use the `basic` scheme with base64-encoded `x-access-token:<token>`; raw or `Bearer` token values fail silently. Canonical examples: [`incremental-pr-approval.yml`](../../.github/workflows/incremental-pr-approval.yml) (`git -c`) and [`git-range-diff.js`](../../.github/scripts/git-range-diff.js) (`GIT_CONFIG_*`).
   - **Fail loudly**: Fetches whose refs are consumed by later steps MUST NOT be wrapped in `|| true` (or otherwise have errors suppressed); a masked failure lets later steps run against stale or missing refs without any signal. Also avoid re-running `--depth=1` fetches after a `fetch-depth: 0` checkout, which re-shallows the fetched ref.


