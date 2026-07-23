# AI Code Review & PR Address Workflow

Standards for branch authoring, session initialization, pull request creation, pre-commit checks, code review, and PR comment address with 100% thread resolution integrity.

---

## 1. Session Initialization & Branch Standards
- **Mandatory Branch Verification**: Check active branch upon session startup. Active branch MUST follow `<prefix>/<kebab-case-description>`.
- **Allowed Branch Prefixes**: `feat/`, `fix/`, `refactor/`, `docs/`, `test/`, `chore/`, `koder/`.
- **Conventional Commit Standard**: All commits MUST follow Conventional Commits format (`<type>(<scope>): <description>`).

## 2. PR Formatting & Mandatory Tags
- **Mandatory Description Tags**: Append `TAG=agy` (and `CONV=<conversation_id>` if present) to all PR descriptions. Place tag-like lines at the very bottom.
- **Description Updates**: Use `./tools/update_pr_description.py <pr_number> --body-file <path>` to update PR bodies.

## 3. Pre-Commit Linters & Log Handling
- **Pre-Commit Hook Execution**: Run `./.githooks/pre-commit` (or `python3 ./tools/lint.py`) and verify zero errors prior to pushing.
- **Forbidden Raw Log Dumps**: Never dump entire build log streams or execution outputs into prompt memory or PR comments. Extract minimal error stack trace snippets.

## 4. AI Code Review Checklist
- **Core Rules Enforcement**: Verify SRP (20-30 line limit), KISS (linear control flow, no redundant else), and default immutability (`const`, shallow copies).
- **Hermetic DI & Mocks**: Verify no `if (useMock)` branches exist in production code; decouple mocks via DI factory providers.
- **Documentation Alignment**: Verify API contracts, CLI flags, and markdown docs are synchronized.

## 5. PR Comment Address & 1-to-1 Resolution
- **Programmatic Comment Enumeration**: Run `./tools/generate_review_checklist.py <owner> <repo> <pr_number>` to parse 100% of open threads and generate `pr_review_checklist.md`.
- **Fetch & Reply Tools**: Use `./tools/fetch_pr_comment.py` to retrieve thread context and `./tools/reply_pr_comment.py` to post replies.
- **1-to-1 Thread Resolution Verification**: Execute `./tools/verify_pr_comment_resolution.py` to programmatically verify every thread received a dedicated response.

## 6. Post-PR Reflection Rules
- **Retrospective Trigger**: Trigger `.agents/skills/pr_workflow/xmc_iterative_reflection/SKILL.md` after resolving high PR comment volume (>3-5 comments) or completing major multi-step feature refactors.
