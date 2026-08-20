# Markdown Documentation Guidelines

This document establishes formatting and maintenance standards for Markdown files across the codebase.

---

## 1. Markdown Formatting & Structure
- **GitHub Flavored Markdown (GFM)**: Follow GitHub Flavored Markdown specifications to ensure consistent rendering across web UIs, editors, and AI agents.
- **Clear Heading Hierarchy**: Maintain clean, logical heading levels (`#`, `##`, `###`). Do not skip heading levels.
- **Fenced Code Blocks**: Always specify the target language identifier on fenced code blocks (e.g., `typescript`, `go`, `python`, `bash`).
- **Clickable Relative Links**: All repository documentation markdown files MUST use standard Markdown relative file links (e.g., [cache config](../deploy/ci/bazel-remote-cache.yaml) or [text](relative/path/file.md)) for internal repository cross-references, and NEVER local filesystem URIs (such as file:///...).
- **Open-Source Compliance & Prohibited Internal Links**: Public repository documentation MUST NEVER contain internal Google corp links (such as `source.corp.google.com`, internal `go/` shortlinks, or internal Google3 depot paths). Always use public GitHub links, relative repository paths, or generic domain placeholders.

## 2. System Contracts & Documentation Alignment
- **Accurate Contract Representation**: Ensure documentation accurately mirrors underlying production specifications, API interfaces, configuration schemas, and CLI flags.
- **Synchronized Updates**: Whenever codebase behavior, API contracts, CLI parameters, or architectural structures change, update all corresponding documentation files within the exact same pull request.
- **Documentation Drift Prevention**: Regularly purge obsolete references, deprecated configuration instructions, or invalid code snippets to keep documentation accurate and useful for human developers and AI agents alike.
