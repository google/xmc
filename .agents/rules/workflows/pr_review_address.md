# AI Code Review & PR Address Workflow

Standards for reviewing pull requests and addressing review comments with 100% thread resolution integrity.

---

## 1. AI Code Review Checklist
- **Core Rules Enforcement**: Verify compliance with SRP (20-30 line function limit), KISS (linear control flow, no redundant else), and default immutability (`const`, `[...arr]`).
- **Hermetic DI & Mocks**: Verify no `if (useMock)` branches exist in production code and mocks use DI factory providers.
- **Documentation Alignment**: Verify API contracts, CLI flags, and repository docs are synchronized.

## 2. Post-PR Creation Standards
- **Branch & Commit Standards**: Require `<prefix>/<kebab-case>` branch names and Conventional Commits (`feat(scope): summary`).
- **Mandatory Tags**: Append `TAG=agy` and `CONV=<conversation_id>` to all PR descriptions.
- **Description Updates**: Use `./tools/update_pr_description.py <pr_number> --body-file <path>` to update PR bodies.

## 3. PR Comment Address Workflow
- **Programmatic Comment Enumeration**: Run `./tools/generate_review_checklist.py <owner> <repo> <pr_number>` to parse 100% of open threads and create `pr_review_checklist.md`.
- **Fetch & Reply Tools**: Use `./tools/fetch_pr_comment.py` to retrieve comment context and `./tools/reply_pr_comment.py` to post replies.
- **1-to-1 Resolution Verification**: Execute `./tools/verify_pr_comment_resolution.py` to programmatically verify all comments received responses before completing the task.
