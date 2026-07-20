# PR Review & Comment Guidelines

This document defines mandatory engineering standards for performing AI Pull Request reviews and managing PR review comment workflows with 100% thread resolution integrity.

---

## 1. AI Code Review Guidelines
- **Comprehensive Rule Enforcement**: When performing Pull Request (PR) reviews, AI agents must enforce all project codebase guidelines, architectural principles (SRP, DRY, KISS, linear control flow), security policies, and language-specific style guides—not merely verify basic syntax or formatting.
- **Review Checklist**:
  - **Single Responsibility & Function Limits**: Verify methods adhere to the 20–30 line target limit and focus on a single responsibility.
  - **Linear Control Flow**: Inspect control flow to ensure early guard clauses are used and no redundant `else` blocks follow terminating return or throw statements.
  - **State Immutability**: Ensure array operations construct shallow copies (`[...arr]`) before mutating, sorting, or filtering signal/observable data.
  - **Dependency Injection & Hermetic Testing**: Confirm production code contains no inline static mock datasets or `if (useMock)` execution flags, verifying mocks are supplied via DI factory providers.
  - **Test Coverage**: Validate that all new features, bug fixes, or code modifications are accompanied by passing unit or integration tests.
  - **Documentation Alignment**: Verify repository markdown docs (including `README.md`, `INSTALL.md`, and component documentation), inline docstrings, and API specifications are updated to reflect any contract adjustments.
- **Actionable & Constructive Feedback**: Provide concrete code suggestions or diff blocks when highlighting non-compliant code during reviews.

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
      print(f"ID: {c[\"id\"]}, Path: {c.get(\"path\")}, Line: {c.get(\"line\") or c.get(\"original_line\")}, Body: {c[\"body\"][:80]}...")
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
- **Required Verification Check**:
  ```bash
  gh api "repos/{owner}/{repo}/pulls/{number}/comments" | python3 -c '
  import json, sys
  comments = json.load(sys.stdin)
  # Verify each target comment ID from checklist has an inline reply or resolved status
  '
  ```
- **Completion Guardrail**: Never mark a PR review comment task as complete until 100% of comment IDs from the checklist are confirmed to have received a dedicated inline response or resolution.
