# PR Review & Inspection Guidelines

This document defines standards for AI agents acting as **Code Reviewers / Inspectors** (evaluating submitted Pull Requests, inspecting diffs, providing review feedback) and managing PR review comment resolution workflows with 100% thread resolution integrity.

---

## 1. Code Review & Inspection Guidelines
- **Comprehensive Rule Enforcement**: When performing Pull Request (PR) reviews, AI agents must enforce all project codebase guidelines, architectural principles (SRP, DRY, KISS, linear control flow), security policies, and language-specific style guides—not merely verify basic syntax or formatting.
- **Contract & API Integrity**: Verify that breaking changes to public APIs, protobuf schemas, CLI flags, or system configurations are explicitly documented and confirmed by the author.
- **Review Checklist & Rule Discovery**: When inspecting code diffs, reviewers must consult [.agents/AGENTS.md](../AGENTS.md) and enforce the relevant rules for the modified files:
  - Enforce foundational guidelines defined in [Core Engineering Standards](core_engineering.md).
  - Enforce language-specific guidelines under `rules/languages/` matching the modified file extensions.
  - Ensure inline documentation, API specifications, and repository markdown docs are synchronized per [Markdown Guidelines](languages/markdown.md).
- **Feedback Quality & Review Presentation**:
  - **Actionable & Constructive Feedback**: Provide concrete code suggestions or diff blocks when highlighting non-compliant code during reviews.
  - **Objective Rules Alignment**: Base review findings explicitly on defined codebase rules rather than personal stylistic preferences.
  - **Context Awareness**: Respect structured agent execution notes (such as Implementation Plans in PR comments) while focusing code reviews strictly on the correctness, security, and maintainability of the submitted code diff.

---

## 2. PR Review Comment Resolution Rules

### A. Mandatory Programmatic Comment Enumeration
- **No Raw Terminal or Log Inspection**: When fetching PR comments via the GitHub API (`gh api repos/{owner}/{repo}/pulls/{number}/comments`), NEVER rely on raw terminal output, unparsed log streams, or manual visual inspection. Raw output can easily truncate or skip comment threads when dealing with lengthy PR discussions.
- **Programmatic Array Parsing**: ALWAYS programmatically parse the full JSON array using Python (`python3 -c "import json..."`) or `jq` to extract 100% of open comment threads, comment IDs, target file paths, and line numbers.
- **Required Enumeration Pattern**:
  ```bash
  gh api "repos/{owner}/{repo}/pulls/{number}/comments" | python3 -c '
  import json, sys
  comments = json.load(sys.stdin)
  print(f"Total Threads Parsed: {len(comments)}")
  for c in comments:
      cid = c.get("id")
      path = c.get("path")
      line = c.get("line") or c.get("original_line")
      body = c.get("body", "")[:80]
      print(f"ID: {cid}, Path: {path}, Line: {line}, Body: {body}...")
  '
  ```

### B. Consolidated Review Checklist Artifact
- **Pre-edit Checklist Artifact**: Before modifying any codebase files, create a complete Markdown checklist artifact containing every detected comment ID, target file path, line range, and requested change.
- **Count Verification**: Explicitly verify the total count of review threads against the parsed JSON array length (`len(comments)`).
- **Required Checklist Structure**:
  ```markdown
  # PR Review Comment Checklist
  - **PR Number**: #{number}
  - **Total Open Threads**: {parsed_array_length}

  ## Review Items
  - [ ] **Comment ID `{id}`** | `{file_path}:{line}`
    - **Requested Change**: {body}
    - **Status**: Pending
  ```

### C. 1-to-1 Thread Resolution Verification
- **Resolution Verification Requirement**: Before marking a PR review task as complete or concluding the workflow, programmatically query the PR's review comments/threads to confirm that every single comment ID from the checklist has received a dedicated inline response or resolution.
- **Required Verification Tool**: Run the dedicated unit-tested resolution verification script in `tools/`:
  ```bash
  ./tools/verify_pr_comment_resolution.py <owner> <repo> <pull_number> <path_to_checklist_file>
  ```
- **Completion Guardrail**: Never mark a PR review comment task as complete until 100% of comment IDs from the checklist are confirmed to have received a dedicated inline response or resolution.

### D. Post-PR Reflection Trigger
- **High PR Comment Volume Trigger**: If resolving PR comments involved a high volume of review feedback (>3-5 comments or multiple review iterations), trigger the [Iterative Reflection](../skills/iterative_reflection/SKILL.md) skill to identify missing project rules or guidelines that led to the review feedback.

