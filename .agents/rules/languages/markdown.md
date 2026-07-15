# Markdown Documentation Guidelines

This document establishes formatting and maintenance standards for Markdown files across the codebase.

---

## 1. Markdown Formatting & Structure
- **GitHub Flavored Markdown (GFM)**: Follow GitHub Flavored Markdown specifications to ensure consistent rendering across web UIs, editors, and AI agents.
- **Clear Heading Hierarchy**: Maintain clean, logical heading levels (`#`, `##`, `###`). Do not skip heading levels.
- **Fenced Code Blocks**: Always specify the target language identifier on fenced code blocks (e.g., ```typescript, ```go, ```python, ```bash).
- **Clickable Relative Links**: Use standard Markdown relative links (`[text](relative/path/file.md)`) for internal repository cross-references.

## 2. System Contracts & Documentation Alignment
- **Accurate Contract Representation**: Ensure documentation accurately mirrors underlying production specifications, API interfaces, configuration schemas, and CLI flags.
- **Synchronized Updates**: Whenever codebase behavior, API contracts, CLI parameters, or architectural structures change, update all corresponding documentation files within the exact same pull request.
- **Documentation Drift Prevention**: Regularly purge obsolete references, deprecated configuration instructions, or invalid code snippets to keep documentation accurate and useful for human developers and AI agents alike.
