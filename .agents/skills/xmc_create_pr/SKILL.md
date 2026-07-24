---
name: XMC Create PR
description: Automates creating and updating GitHub Pull Requests, running pre-commit hooks, formatting PR body templates, and managing CI wait buffers.
---

# GitHub Pull Request Creation & Update Playbook

This skill provides step-by-step procedures for creating, updating, and managing GitHub Pull Requests in the XMC repository.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [GitHub Authoring & PR Creation Standards](../../rules/github_authoring_and_prs.md), [Skills & Automation Guidelines](../../rules/skills_and_automation.md), and [Core Engineering Standards](../../rules/core_engineering.md).

---

## Guidelines for Using CLI Tools

When invoking helper tools under `tools/` (e.g., `update_pr_description.py`):
1. **Do not read tool source code**: Do not open or read the Python source code of helper tools. Refer to [Skills & Automation Guidelines](../../rules/skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints).
2. **Use `--help`**: Run tools with `--help` to inspect parameters and usage.
3. **Parse Structured Output & Handle Errors**: All tools output structured JSON (exit code 0 for success, 1 for handled error, 2 for unhandled error).
4. **Shell & Payload Safety**: Execute standalone `.py` script files or pass arguments via script parameters. Avoid double-quoted inline commands (`python3 -c "..."`).

---

## 1. Branch Check & Formatting Verification

Before making commits or opening a PR:

1. **Check current branch**:
   ```bash
   git branch --show-current
   ```
2. **Validate branch format**:
   Ensure the branch name strictly conforms to `<prefix>/<kebab-case-description>` (e.g., `feat/enforce-pr-comment-integrity`, `fix/gke-rollout-timeout`).
3. **Rename branch if non-compliant**:
   If the branch name uses raw worktree directory names, underscores, or lacks a prefix:
   ```bash
   git branch -m <prefix>/<kebab-case-description>
   ```
4. **Detached HEAD State**:
   If `git branch --show-current` is empty, create and switch to a compliant branch:
   ```bash
   git checkout -b <prefix>/<kebab-case-description>
   ```

---

## 2. Pre-Commit Hooks & Commit Creation

Before creating any commit:

1. **Run Pre-Commit Formatting Protocol**:
   ```bash
   ./.githooks/pre-commit && git add .
   ```
2. **Format Conventional Commit Message**:
   Format the commit message header using `<type>(<scope>): <summary>` (e.g., `feat(auth): add OAuth2 token refresh support`).
3. **Include Mandatory Context & Tags**:
   Append technical rationale, bug references (e.g., `Fixes bug 12345`), and mandatory description tags at the bottom of the commit body:
   ```markdown
   TAG=agy
   CONV=<conversation_id>
   ```
4. **Execute Git Commit via Message File**:
   To avoid shell double-quoting syntax errors when dealing with multiline commit bodies, write the commit message to a temporary file and commit using `-F`:
   ```bash
   cat << 'EOF' > commit_msg.txt
   <type>(<scope>): <summary>

   <Detailed commit body>

   TAG=agy
   CONV=<conversation_id>
   EOF
   git commit -F commit_msg.txt && rm commit_msg.txt
   ```

---

## 3. Pull Request Creation & PR Body Population

When creating a new PR or populating its description:

1. **Pre-Commit Verification**: Verify Bazel buildifier, Go formatter, addlicense, and Ruff Python linter pass cleanly.
2. **Conventional PR Title**: Match the PR title to Conventional Commits syntax (`<type>(<scope>): <summary>`).
3. **Format PR Body Template**: Write the PR body to a temporary markdown file (e.g., `pr_body.md`) following the standard template:
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
4. **Create PR via GitHub CLI**:
   ```bash
   gh pr create --title "<type>(<scope>): <summary>" --body-file pr_body.md
   ```

---

## 4. PR Description Updates via Helper Tool

When updating an existing Pull Request description:

1. **Format Updated Body**: Prepare the updated body markdown in a temporary file (e.g., `updated_pr_body.md`).
2. **Execute Update Tool**:
   ```bash
   ./tools/update_pr_description.py <pr_number> --body-file updated_pr_body.md
   ```
   > [!IMPORTANT]
   > Do NOT use `gh pr edit` directly to update descriptions, as classic project deprecations break description fields.

---

## 5. CI Wait Buffer Period

1. After creating or updating a non-draft Pull Request, pause execution for approximately **2 minutes** (120 seconds).
2. Allow GitHub CI status checks and automated PR review passes to run and produce initial status checks before proceeding to subsequent workflow steps.
