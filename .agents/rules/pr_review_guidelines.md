# PR Review & Inspection Guidelines

This document defines standards for AI agents acting as **Code Reviewers / Inspectors** (evaluating submitted Pull Requests, inspecting diffs, providing review feedback) and managing PR review comment resolution workflows with 100% thread resolution integrity.

---

## 1. Code Review & Inspection Guidelines
- **Comprehensive Rule Enforcement**: When performing Pull Request (PR) reviews, AI agents must enforce all project codebase guidelines, architectural principles (SRP, DRY, KISS, linear control flow), security policies, and language-specific style guides—not merely verify basic syntax or formatting.
- **Contract & API Integrity**: Verify that breaking changes to public APIs, protobuf schemas, CLI flags, or system configurations are explicitly documented and confirmed by the author.
- **Assumptions Disclosure Audit**: Verify the PR body contains a populated `## Assumptions` section per [Mandatory Assumptions Disclosure](github_authoring_and_prs.md#3-pull-request-creation--pr-sizing-standards). Flag a missing section, and challenge disclosures that are vague (`assumed the existing behavior is correct`), omit the impact-if-wrong clause, or list something the author could have verified from the diff. Where an assumption is load-bearing for correctness or a system contract, request verification or explicit user sign-off before approval. Conversely, treat an implementation choice the reviewer must reverse-engineer from the diff as an undisclosed assumption.
- **Review Checklist & Rule Discovery**: When inspecting code diffs, reviewers must check modified file extensions (e.g., via `git diff --name-only`) and enforce the matching language guidelines before providing review feedback:
  - Enforce foundational guidelines defined in [Core Engineering Standards](core_engineering.md), including the strict prohibition of generic dumping-ground names (`util`, `helper`, `misc`, etc.) and compound suffixes (`urlutil`, `stringhelper`) for files, methods, packages, or directories. Flag any generic naming during review as an SRP violation.
  - Enforce language-specific guidelines under `rules/languages/` matching modified file extensions: [Python Guidelines](languages/python.md), [Go Guidelines](languages/golang.md), [TypeScript & Modern Angular Guidelines](languages/typescript_javascript.md), [Shell Scripting Guidelines](languages/shell.md), [Markdown Guidelines](languages/markdown.md).
  - Ensure inline documentation, API specifications, and repository markdown docs are synchronized per [Markdown Guidelines](languages/markdown.md).
- **Test Fixture & Telemetry Wire-Up Verification**: When reviewing PRs adding new services, capabilities, or telemetry recorders (e.g., `GatewayMetrics`), verify that test constructors do not silently bypass dependencies with `nil` or `null` per [Core Engineering Standards](core_engineering.md#4-high-fidelity-test-doubles--decoupling) and that test cases assert expected side effects (metric counters, log events, state changes).
- **Feedback Quality & Review Presentation**:
  - **Actionable & Constructive Feedback**: Provide concrete code suggestions or diff blocks when highlighting non-compliant code during reviews.
  - **Objective Rules Alignment**: Base review findings explicitly on defined codebase rules rather than personal stylistic preferences.
  - **Context Awareness**: Respect structured agent execution notes (such as Implementation Plans in PR comments) while focusing code reviews strictly on the correctness, security, and maintainability of the submitted code diff.
- **Interactive Multi-Agent Review Workflow (Optional)**: For structured PR code reviews featuring dynamic expert subagent panels, open user direction windows, inline comment deduplication, explicit confirmation gates, and re-review verification, agents may optionally utilize the procedure defined in the [XMC Review PR Skill](../skills/xmc_review_pr/SKILL.md) backed by `./tools/pr_review/review_pr_generate_audit.py`, `./tools/pr_review/review_pr_post_inline_comment.py`, and `./tools/pr_review/review_pr_verify_posted.py`, or use their own established review workflows.

---

## 2. PR Review Comment Resolution Rules

For detailed comment resolution workflows, including fetching comment context, generating checklist artifacts, running local unit tests, and verifying 1-to-1 thread resolution, follow the mandatory procedures defined in the [Address PR Comment Skill](../skills/xmc_address_pr_comment/SKILL.md).

Key tools for PR comment processing:
- **Generate Review Checklist Artifact**: `./tools/pr_review/generate_review_checklist.py <owner> <repo> <pull_number> [-o pr_review_checklist.md]`
- **Fetch Inline Comment Context**: `./tools/pr_review/fetch_pr_comment.py "<comment_url>"`
- **Post Thread Reply**: `./tools/pr_review/reply_pr_comment.py "<comment_url>" "<reply_message>"`
- **Verify Thread Resolution**: `./tools/pr_review/verify_pr_comment_resolution.py <owner> <repo> <pull_number> <path_to_checklist_file>`

