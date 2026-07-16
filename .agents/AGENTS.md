# Core Engineering & Multi-Agent Coordination Guidelines

This root guidelines document defines high-level universal engineering principles across the codebase and provides an organized index to all modular rule specifications located under `.agents/rules/`.

---

## Universal Architectural Principles

1. **Single Responsibility Principle (SRP)**: Keep all modules, classes, functions, and skills focused on a single responsibility. Decompose complex logic into small pure helpers (aim for a 20–30 line target limit).
2. **Keep It Simple, Stupid (KISS)**: Enforce linear control flow. Use early return guard clauses at the top of functions and eliminate redundant `else` blocks after returns or exceptions.
3. **Don't Repeat Yourself (DRY)**: Centralize logic, eliminate duplicate code patterns, and extract shared automation playbooks into reusable Python modules or dedicated rule files.
4. **Context Preservation & Subagent Delegation**: Prevent main context bloat by modularizing rules into purpose-built files and offloading verbose log analysis, heavy test runs, or exploratory searches to worker subagents.
5. **Documentation Alignment**: Synchronize relevant repository markdown files (such as `README.md`, `INSTALL.md`, and component docs), inline comments, schemas, and skills in the same PR whenever system behavior or API contracts change.

---

## Modular Rules Index

### Core Engineering & Operational Workflows
- 📘 [Core Engineering Standards](rules/core_engineering.md): SRP, KISS, 20–30 line function limit, linear control flow, default immutability, high-fidelity test doubles, strict coverage, atomic PRs, and contract update rules.
- 🛠️ [Skills & Automation Guidelines](rules/skills_and_automation.md): Extracting complex logic into testable Python scripts, applying DRY/KISS to skills, preventing context creep, and delegating work to subagents.
- 🔍 [PR Review & Post-PR Workflow](rules/pr_review_and_github.md): Code review checklist for AI reviews and post-PR creation feedback loop guidelines (~2 min wait for GitHub PR bot, comment fetching, fix proposals).

### Language-Specific Rules
- 🐹 [Go (Golang)](rules/languages/golang.md): Idiomatic Go style, clean interfaces, lowercase error wrapping (`%w`), concurrency safety (`context.Context`, mutexes), and table-driven testing.
- 🟦 [TypeScript & Modern Angular](rules/languages/typescript_javascript.md): Prefer TypeScript over JavaScript, TypeScript strictness, modern Angular signals and control flow (`@if`, `@for`, `@switch`), state immutability, declarative styling maps, and DI polymorphism.
- 🐍 [Python](rules/languages/python.md): PEP 8 Python style guide, standard library preference, explicit public API type hints, docstrings, `unittest` framework, and Python tooling over shell scripts.
- 🐚 [Shell Scripting](rules/languages/shell.md): Minimal shell script scope (`set -euo pipefail`), double-quoting variables (`"$VAR"`), restricting Shell to thin entrypoints while deferring logic to Python.
- 📝 [Markdown Documentation](rules/languages/markdown.md): GFM formatting, heading hierarchy, clickable relative links, accurate contract representation, and synchronized documentation updates.
