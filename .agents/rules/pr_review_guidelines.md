# Code Review & Inspection Guidelines

This document defines standards for AI agents acting as **Code Reviewers / Inspectors** (evaluating submitted Pull Requests, inspecting diffs, and providing automated review comments).

---

## 1. Review Scope & Standards Enforcement
- **Comprehensive Rule Enforcement**: When performing Pull Request (PR) reviews, AI agents must enforce all project codebase guidelines, architectural principles, security policies, and language-specific style guides—not merely verify basic syntax or formatting.
- **Contract & API Integrity**: Verify that breaking changes to public APIs, protobuf schemas, or system configurations are explicitly documented and confirmed by the author.

## 2. Review Checklist & Rule Discovery
When inspecting code diffs, reviewers must consult [.agents/AGENTS.md](../AGENTS.md) and enforce the relevant rules for the modified files:
- Enforce foundational guidelines defined in [Core Engineering Standards](core_engineering.md).
- Enforce language-specific guidelines under `rules/languages/` matching the modified file extensions.
- Ensure inline documentation, API specifications, and repository markdown docs are synchronized per [Markdown Guidelines](languages/markdown.md).

## 3. Feedback Quality & Review Presentation
- **Actionable & Constructive Feedback**: Provide concrete code suggestions or diff blocks when highlighting non-compliant code during reviews.
- **Objective Rules Alignment**: Base review findings explicitly on defined codebase rules rather than personal stylistic preferences.
- **Context Awareness**: Respect structured agent execution notes (such as Implementation Plans in PR comments) while focusing code reviews strictly on the correctness, security, and maintainability of the submitted code diff.
