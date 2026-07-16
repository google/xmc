# PR Review & GitHub Bot Workflow Rules

This document defines standards for automated AI Pull Request reviews and GitHub PR bot workflows.

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

## 2. GitHub PR Bot Workflow Guidelines
- **Non-Draft PR Trigger**: Initiate automated review and verification loops when a non-draft Pull Request is created or updated.
- **Automated Buffer Period**: Wait approximately **2 minutes** after non-draft PR creation before starting automated review passes to allow CI status checks and automated indexing triggers to settle.
- **Proactive Comment Processing**: Automatically fetch all incoming inline review comments, requested changes, and thread feedback using available repository tools (e.g., `tools/fetch_pr_comment.py` or GitHub CLI).
- **Fix Proposal & Verification**: Formulate clear fix plans, implement required adjustments, run local test suites to verify resolutions, and propose fix diffs for user confirmation before pushing updates.
