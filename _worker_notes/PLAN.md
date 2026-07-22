# Implementation Plan - Synthesis

## Objective
Refine `.agents/rules/core_engineering.md` section 7 (`## 7. Subagent Inspection & Workspace File Guidelines`) based on user design feedback:
- Clarify that `diff` / `git diff` is the preferred tool for comparing code deltas across files, branches, and workspaces (pre-approved in permission allowlist).
- Mention `view_file` for inspecting full file content context when reading specific line ranges.

## Implementation Steps
1. [x] Review prior attempts (Worker 1 and Worker 2) and record findings in `_worker_notes/REVIEW.md`.
2. [x] Sync branch `feat/rules-and-tools-optimization` with `origin/feat/rules-and-tools-optimization`.
3. [x] Ensure `.agents/rules/core_engineering.md` section 7 has the clear, action-oriented bullet titles:
   - `- **Prefer `diff` / `git diff` for Code Comparison**: ...`
   - `- **Use `view_file` for Full File Content Context**: ...`
   - `- **Creating Non-Artifact Workspace Documentation**: ...`
4. [ ] Run `./.githooks/pre-commit` to verify code quality and linter compliance.
5. [ ] Stage modified files (`git add -u`).
6. [ ] Verify commit message and tags:
   `docs(agents): prefer diff for code comparison across workspaces`
   `TAG=agy`
   `CONV=013d9e06-1a8a-491f-b70a-c739c354c1a0`
7. [ ] Push to `origin/feat/rules-and-tools-optimization` updating Draft PR #183.
8. [ ] Write handoff notes in `_worker_notes/README.md`.
9. [ ] Report completion to caller via `send_message`.
