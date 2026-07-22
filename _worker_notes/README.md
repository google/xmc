# Worker Handoff Notes (`_worker_notes/README.md`)

## Status & What Works
- Section 7 (`## 7. Subagent Inspection & Workspace File Guidelines`) in `.agents/rules/core_engineering.md` has been refined based on user feedback.
- It clearly specifies that `diff` and `git diff` (pre-approved in allowlist) are preferred for line-by-line code comparisons across files, branches, and workspaces.
- It specifies `view_file` for inspecting full file context when reading line ranges.
- Pre-commit linter hooks (`./.githooks/pre-commit`) pass cleanly.

## Verification / Running Checks
Run the pre-commit checks:
```bash
./.githooks/pre-commit --check
```
