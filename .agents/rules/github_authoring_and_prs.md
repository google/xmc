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
- **Session Initialization Branch Check Policy**: Immediately upon starting a task, agents MUST verify that the current branch strictly conforms to the `<prefix>/<kebab-case-description>` format. Non-compliant branch names (or detached HEAD states) MUST be renamed/checkout to a compliant branch before making code changes or rebasing. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#1-branch-check--formatting-verification) for procedure.
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

## 3. Pull Request Creation & Field Formatting Policy
- **Mandatory User Confirmation Gate Before PR Creation & Publishing**:
  1. Creating any PR (including Draft PR `gh pr create --draft`) requires explicit, affirmative user confirmation in the current conversation turn.
  2. Marking a Draft PR as Ready for Review (`gh pr ready <pr_number>`) ALSO requires explicit user confirmation.
  3. The confirmation prompt template MUST include the planned PR Title, PR Body Preview (from `pr_body.md`), and list of changes.
  Phrases like "proceed with next phase" authorize local work and verification, but DO NOT authorize opening or publishing a remote Pull Request.
- **Pre-Commit Verification**: Before committing or opening a PR, ensure all repository linter hooks pass (Bazel buildifier, Go formatter, addlicense, Ruff Python linter) and unit tests pass cleanly.
- **Conventional PR Titles**: Match the PR title to Conventional Commits syntax (`<type>(<scope>): <summary>`).
- **Mandatory PR Body Population**: ALWAYS populate the official PR Body/Description field upon creation with full context, summary, architectural intent, test verification results, and issue references.
- **Mandatory PR Description Update Tool Policy**: When updating an existing PR description, ALWAYS use `tools/update_pr_description.py` via the procedure detailed in [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#4-pr-description-updates-via-helper-tool). Do NOT use `gh pr edit` or raw inline shell commands, as GraphQL classic projects deprecation breaks `gh pr edit`.
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

  ## Verification & Testing
  - **Commands Executed**: `<bazel test / go test / ./tools/run_affected_tests.py>`
  - **Coverage & Test Status**: <Summary of unit/integration test results and pass state.>

  ## Related Issues / Bugs
  Fixes bug <bug_id> (or N/A)

  TAG=agy
  CONV=<conversation_id>
  ```
- **PR Granularity & Single Responsibility**: Limit each PR to a single, cohesive change. Avoid bundling unrelated code refactoring, whitespace reformatting, or dependency upgrades inside feature/bugfix PRs.

## 4. Post-PR Creation Workflow & Addressing Review Feedback Policy
- **Wait Buffer Policy After PR Creation**: When instructed to create or update a non-draft Pull Request, after creating the PR, agents MUST pause for approximately **2 minutes** to allow GitHub CI status checks and automated GitHub PR review passes to complete. Refer to [XMC Create PR Skill](../skills/xmc_create_pr/SKILL.md#5-ci-wait-buffer-period).
- **Addressing Review Comments**: When review feedback is received, execute the detailed comment resolution workflow defined in the [Address PR Comment Skill](../skills/xmc_address_pr_comment/SKILL.md) (fetching comment context, generating checklist artifacts, running local tests, confirming contract changes with the user, posting inline thread replies, and verifying 1-to-1 resolution).
- **Prohibition of Force-Pushing on PR Branches**: NEVER use force-push (`git push --force` or `git push --force-with-lease`) when pushing fix commits or addressing review comments on an active Pull Request branch. Always append incremental commits to the branch so reviewers can easily inspect diffs before and after changes between review passes.
