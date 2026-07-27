# Core Engineering & Multi-Agent Coordination Guidelines

This root guidelines document defines high-level universal engineering principles across the codebase and provides an organized index to all modular rule specifications located under `.agents/rules/`.

---

## Universal Architectural Principles

1. **Single Responsibility Principle (SRP)**: Keep all modules, classes, functions, and skills focused on a single responsibility. Decompose complex logic into small pure helpers (aim for a 20–30 line target limit).
2. **Keep It Simple, Stupid (KISS)**: Enforce linear control flow. Use early return guard clauses at the top of functions and eliminate redundant `else` blocks after returns or exceptions.
3. **Don't Repeat Yourself (DRY)**: Centralize shared helpers in global packages (e.g., `proto.Modify`), eliminate duplicate code and test fixtures, and extract shared automation playbooks into reusable Python modules or dedicated rule files.
4. **Context Preservation & Subagent Delegation**: Prevent main context bloat by modularizing rules into purpose-built files and offloading verbose log analysis, heavy test runs, or exploratory searches to worker subagents.
5. **Documentation Alignment**: Synchronize relevant repository markdown files (such as `README.md`, `INSTALL.md`, and component docs), inline comments, schemas, and skills in the same PR whenever system behavior or API contracts change.
6. **Iterative Reflection & Meta-Learning**: After major implementations, PRs with high review comment volume (>3-5 comments), or user request, audit session trajectory using the [Iterative Reflection Skill](skills/xmc_iterative_reflection/SKILL.md).

---

## Routing Matrices

### File Extension Routing Matrix
Before modifying or reviewing code, check the file extensions of touched files and load the mandatory language guidelines:

| File Extension(s) | Mandatory Guideline File |
| :--- | :--- |
| `*.py` | [Python Guidelines](rules/languages/python.md) |
| `*.go` | [Go Guidelines](rules/languages/golang.md) |
| `*.ts`, `*.js`, `*.html` | [TypeScript & Modern Angular Guidelines](rules/languages/typescript_javascript.md) |
| `*.sh`, `*.bash` | [Shell Scripting Guidelines](rules/languages/shell.md) |
| `*.md` | [Markdown Guidelines](rules/languages/markdown.md) |

### Task Intent Routing Matrix
Identify the primary task type to load mandatory workflow rules and skills based on explicit user prompt patterns:

| Task Intent / Workflow | Trigger Keywords & User Prompt Patterns | Primary Workflow Rule / Skill |
| :--- | :--- | :--- |
| **PR Review / Inspection** | `Review PR`, `Inspect diff`, `Audit code changes` | [PR Review Guidelines](rules/pr_review_guidelines.md) |
| **PR Creation & Authoring** | `Create PR`, `Open pull request`, `Submit PR`, `Update PR description`, `Publish PR` | [XMC Create PR Skill](skills/xmc_create_pr/SKILL.md) & [GitHub Authoring Standards](rules/github_authoring_and_prs.md) |
| **Addressing PR Comments** | `Address PR comment`, `Fix review comment`, `Resolve PR comments`, `Process review feedback` | [Address PR Comment Skill](skills/xmc_address_pr_comment/SKILL.md) & [PR Review Guidelines](rules/pr_review_guidelines.md) |
| **Feature Development & Refactoring** | `Implement b/...`, `Implement feature`, `Add feature`, `Fix bug`, `Fix b/...`, `Refactor package/code`, `Develop endpoint` | [XMC Feature Development Skill](skills/xmc_feature_development/SKILL.md) & [Core Engineering Standards](rules/core_engineering.md) |
| **Deploy Environment** | `Deploy environment`, `Setup XMC environment`, `Deploy Cluster Toolkit blueprint` | [Deploy XMC Skill](skills/xmc_deploy/SKILL.md) |
| **Upgrade Environment** | `Upgrade environment`, `Sync deployed env to HEAD`, `Upgrade XMC on GKE` | [Upgrade XMC Skill](skills/xmc_upgrade/SKILL.md) |
| **Cleanup Environment** | `Clean up environment`, `Tear down environment`, `Destroy XMC cluster`, `Delete dev environment` | [Cleanup Environment Skill](skills/xmc_cleanup_environment/SKILL.md) |
| **Reflection & Meta-Learning** | `Reflect on session`, `Run session retrospective`, `Audit prompt performance` | [Iterative Reflection Skill](skills/xmc_iterative_reflection/SKILL.md) |

> [!NOTE]
> **Workflow Path Selection Policy (Fast-Path Exemptions)**:
> - **Full Skill Path**: Apply full skill playbooks (coverage checks, affected test runs, CI wait buffers, GKE redeployments) for major features, complex multi-comment reviews, and environment setups.
> - **Lightweight Fast-Path Exception**: For minor bug fixes (<10 lines), documentation updates, or prototyping spikes:
>   - Skip Bazel `rdeps` and full coverage reports in feature development.
>   - Skip GKE redeployment steps in PR comment resolution unless cloud redeployment is explicitly requested.
>   - Skip draft/wait buffers when direct PR submission is requested.

---

## Modular Rules Index

### Core Engineering & Operational Workflows
- 📘 [Core Engineering Standards](rules/core_engineering.md): SRP, KISS, 20–30 line function limit, linear control flow, default immutability, centralized shared helpers, baseline test fixtures, high-fidelity test doubles, strict coverage, atomic PRs, and post-implementation reflection rules.
- 🛠️ [Skills & Automation Guidelines](rules/skills_and_automation.md): Extracting complex logic into testable Python scripts, applying DRY/KISS to skills, preventing context creep, and delegating work to subagents.

### GitHub & PR Workflows
- 🚀 [GitHub Authoring & PR Creation](rules/github_authoring_and_prs.md): Git branch prefixes (`feat/`, `fix/`, `refactor/`, `docs/`, `test/`, `chore/`, `koder/`), mandatory session initialization branch check policy, Conventional Commit standards, PR title & body formatting rules, mandatory tags (`TAG=agy`), pre-commit linter policy, forbidden raw log dumps, and CI wait buffer policy.
- 📦 [XMC Create PR Skill](skills/xmc_create_pr/SKILL.md): Playbook for checking branch format, running pre-commit hooks, formatting PR body templates, updating PR descriptions via helper script, and managing CI wait buffers.
- ⚡ [XMC Feature Development Skill](skills/xmc_feature_development/SKILL.md): Playbook for requirements scoping, clean code design, running affected test matrices, measuring Bazel coverage, and pre-commit verification.
- 📋 [Code Review & Comment Guidelines](rules/pr_review_guidelines.md): AI code review checklists (SRP, KISS, immutability, DI testing, test coverage, doc sync), review feedback standards, mandatory PR comment resolution rules, and post-PR reflection triggers.

### Language-Specific Rules
- 🐹 [Go (Golang)](rules/languages/golang.md): Idiomatic Go style, clean interfaces, lowercase error wrapping (`%w`), concurrency safety (`context.Context`, mutexes), table-driven testing with baseline fixtures (`proto.Modify`), and centralized test utilities.
- 🟦 [TypeScript & Modern Angular](rules/languages/typescript_javascript.md): Prefer TypeScript over JavaScript, TypeScript strictness, modern Angular signals and control flow (`@if`, `@for`, `@switch`), state immutability, declarative styling maps, and DI polymorphism.
- 🐍 [Python](rules/languages/python.md): PEP 8 Python style guide, Python 3.14+ version enforcement, standard library preference, explicit public API type hints, docstrings, `unittest` framework, Python tooling over shell scripts, and local virtual environment requirements.
- 🐚 [Shell Scripting](rules/languages/shell.md): Minimal shell script scope (`set -euo pipefail`), double-quoting variables with braces (`"${VAR}"`), restricting Shell to thin entrypoints while deferring logic to Python.
- 📝 [Markdown Documentation](rules/languages/markdown.md): GFM formatting, heading hierarchy, clickable relative links, accurate contract representation, and synchronized documentation updates.
