# PR Review Comment Workflow Guidelines

This document defines mandatory engineering guidelines for handling GitHub Pull Request review comments, preventing partial inspection of long comments, and enforcing 100% thread resolution integrity.

---

## Mandatory PR Review Comment Rules

### 1. Mandatory Programmatic Comment Enumeration
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

### 2. Consolidated Review Checklist
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

### 3. 1-to-1 Thread Resolution Verification
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
