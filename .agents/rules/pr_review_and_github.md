# PR Review & Post-PR Creation Workflow Rules

This document defines standards for AI Pull Request reviews and post-PR creation feedback loops for local agents.

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

## 2. Post-PR Creation Workflow for Local Agents
- **Wait Buffer After PR Creation**: When instructed to create or update a non-draft Pull Request, after creating the PR, wait approximately **2 minutes** to allow GitHub CI status checks and automated GitHub PR review passes to complete.
- **Proactive Comment Fetching**: After the 2-minute wait buffer, proactively fetch all incoming inline review comments, requested changes, and thread feedback using repository tools (e.g., `tools/fetch_pr_comment.py` or GitHub CLI `gh api`).
- **Validate Comments & Check Contract Changes**:
  - Evaluate incoming review comments for technical validity and applicability before proceeding.
  - If addressing a comment alters a PR contract, public API specification, or product behavior, **alert the user and seek explicit confirmation first** before modifying code.
- **Addressing PR Comments Workflow**: Follow the mandatory rules defined in [PR Review Guidelines](pr_review_guidelines.md) (programmatic comment enumeration, consolidated review checklist artifact, and 1-to-1 thread resolution verification) and the detailed comment resolution workflow in the [Address PR Comment](../skills/address_pr_comment/SKILL.md) skill.
