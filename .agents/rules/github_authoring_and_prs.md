# GitHub Authoring & PR Creation Standards

This document defines standards for AI agents acting as **Code Authors / Contributors** (writing code, managing branches, generating commits, creating pull requests, and addressing review comments).

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
- **Forbidden Branch Naming Anti-Patterns**:
  - NEVER use un-prefixed branch names (e.g., `enforce_pr_comment_integrity`).
  - NEVER rely on raw worktree directory names or task IDs without verifying `<prefix>/<kebab-case-description>` format.
  - NEVER use underscores (`_`) in branch names; always use hyphens (`-`) for word separators.

## 2. Git Commits & Conventional Commit Messages
- **Conventional Commit Format**: Format commit subject lines using Conventional Commits syntax:
  `<type>(<scope>): <summary>` (e.g., `feat(auth): add OAuth2 token refresh support` or `fix(reconciler): resolve deadlock on pod restart`).
  - Allowed types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`.
  - Write subject lines in the imperative mood, using lowercase, and without trailing periods.
- **Detailed Commit Body**: Provide context in the commit body describing *why* the changes were made, architectural choices, and technical rationale—not merely a list of modified files.
- **Traceability & References**:
  - When addressing review comments, include the comment link in the commit body: `Comment link: <github_comment_url>`.
  - When referencing bugs, include the bug ID in plain text (e.g., `Fixes bug <bug_id>`, `Closing bug <bug_id>`, or `Bug: <bug_id>`).
- **Mandatory Description Tags**: All CL/PR descriptions MUST end with mandatory system tags placed at the very bottom after all human-readable text:
  ```markdown
  TAG=agy
  CONV=<conversation_id>
  ```
- **Atomic Commits**: Keep commits logical, focused, and self-contained. Each commit must compile and pass relevant local tests.

## 3. Pull Request Creation & Field Formatting
- **Pre-Commit Verification**: Before committing or opening a PR, ensure all repository linter hooks pass (Bazel buildifier, Go formatter, addlicense, Ruff Python linter) and unit tests pass cleanly.
- **Conventional PR Titles**: Match the PR title to Conventional Commits syntax (`<type>(<scope>): <summary>`).
- **PR Description Field (PR Body) vs. PR Comments**:
  - **Mandatory PR Body Population**: ALWAYS populate the official PR Body/Description field upon creation with full context, summary, architectural intent, test verification results, and issue references.
  - **Reserved Purpose of PR Body vs. Comments**: The PR body holds the primary executive summary and structural contract. PR comments are reserved for discussion, review thread resolutions, and automated agent execution context.
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
  - **Commands Executed**: `<bazel test / go test / python3 -m unittest command>`
  - **Coverage & Test Status**: <Summary of unit/integration test results and pass state.>

  ## Related Issues / Bugs
  Fixes bug <bug_id> (or N/A)

  TAG=agy
  CONV=<conversation_id>
  ```
- **PR Granularity & Single Responsibility**: Limit each PR to a single, cohesive change. Avoid bundling unrelated code refactoring, whitespace reformatting, or dependency upgrades inside feature/bugfix PRs.

## 4. Post-PR Creation Workflow & Addressing Review Feedback
- **Wait Buffer After PR Creation**: When instructed to create or update a non-draft Pull Request, after creating the PR, wait approximately **2 minutes** to allow GitHub CI status checks and automated GitHub PR review passes to complete.
- **Proactive Comment Fetching**: After the 2-minute wait buffer, proactively fetch all incoming inline review comments, requested changes, and thread feedback using repository tools (e.g., `tools/fetch_pr_comment.py` or GitHub CLI `gh api`).
- **Validate Comments & Check Contract Changes**:
  - Evaluate incoming review comments for technical validity and applicability before proceeding.
  - If addressing a comment alters a PR contract, public API specification, or product behavior, **alert the user and seek explicit confirmation first** before modifying code.
- **Addressing PR Comments Workflow**: Follow the mandatory rules defined in [PR Review Guidelines](pr_review_guidelines.md) (programmatic comment enumeration, consolidated review checklist artifact, and 1-to-1 thread resolution verification) and the detailed comment resolution workflow in the [Address PR Comment](../skills/address_pr_comment/SKILL.md) skill.
- **PR Comment Replies**: Keep GitHub comment thread replies concise, objective, and focused on resolution (e.g., `"code changed"` or summarizing test verification), avoiding noisy or redundant chatter in PR review threads.
