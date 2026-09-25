# Core Engineering & Multi-Agent Coordination Guidelines

This root guidelines document defines high-level universal engineering principles across the codebase and provides an organized index to all modular rule specifications located under `.agents/rules/`.

---

## Universal Architectural Principles

1. **Single Responsibility Principle (SRP) & Explicit Domain Naming**: Keep all modules, classes, functions, and skills focused on a single responsibility. Prohibit generic names (`util`, `helper`, `misc`, `common`) for files, methods, or folders—including compound suffixes/prefixes like `urlutil` or `stringhelper`—as temptation to use generic names signals an SRP violation (see [Core Engineering Standards](rules/core_engineering.md)). Decompose complex logic into small pure functions (aim for a 20–30 line target limit).
2. **Keep It Simple, Stupid (KISS)**: Enforce linear control flow. Use early return guard clauses at the top of functions and eliminate redundant `else` blocks after returns or exceptions.
3. **Don't Repeat Yourself (DRY)**: Centralize shared helpers in global packages (e.g., `proto.Modify`), eliminate duplicate code and test fixtures, and extract shared automation playbooks into reusable Python modules or dedicated rule files.
4. **Context Preservation & Subagent Delegation**: Prevent main context bloat by modularizing rules into purpose-built files and offloading verbose log analysis, heavy test runs, or exploratory searches to worker subagents.
5. **Documentation Alignment**: Synchronize relevant repository markdown files (such as `README.md`, `INSTALL.md`, and component docs), inline comments, schemas, and skills in the same PR whenever system behavior or API contracts change.
6. **Iterative Reflection & Meta-Learning**: Following PR review comment resolutions, major implementations, or user request, audit session trajectories and review feedback using the [Iterative Reflection Skill](skills/xmc_iterative_reflection/SKILL.md) and validate candidate rule improvements with the [Trajectory Comparison Skill](skills/xmc_trajectory_comparison/SKILL.md).

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
Identify the primary task type to load the mandatory workflow rules and skills:

| Task Intent / Workflow | Primary Workflow Rule / Skill |
| :--- | :--- |
| PR Review / Inspection | [XMC Review PR Skill](skills/xmc_review_pr/SKILL.md) & [PR Review Guidelines](rules/pr_review_guidelines.md) |
| PR Creation & Authoring | [XMC Create PR Skill](skills/xmc_create_pr/SKILL.md) & [GitHub Authoring Standards](rules/github_authoring_and_prs.md) |
| PR Sizing & Vertical Splitting | [XMC PR Split Verification Skill](skills/xmc_pr_split_verification/SKILL.md) & [GitHub Authoring Standards](rules/github_authoring_and_prs.md) |
| Addressing PR Comments | [Address PR Comment Skill](skills/xmc_address_pr_comment/SKILL.md) & [PR Review Guidelines](rules/pr_review_guidelines.md) |
| Feature Development & Refactoring | [XMC Feature Development Skill](skills/xmc_feature_development/SKILL.md) & [Core Engineering Standards](rules/core_engineering.md) |
| Release Orchestration | [XMC Release Skill](skills/xmc_release/SKILL.md) & [Core Engineering Standards](rules/core_engineering.md) |
| Release Notes Generation | [XMC Generate Release Notes Skill](skills/xmc_generate_release_notes/SKILL.md) & [Release Notes Taxonomy](skills/xmc_generate_release_notes/references/release_notes_taxonomy.md) |
| Deploy / Upgrade / Cleanup Environment | [Deploy XMC Skill](skills/xmc_deploy/SKILL.md), [Upgrade XMC Skill](skills/xmc_upgrade/SKILL.md), [Cleanup Environment Skill](skills/xmc_cleanup_environment/SKILL.md) |
| Reflection & Meta-Learning | [Iterative Reflection Skill](skills/xmc_iterative_reflection/SKILL.md) & [Rule Authoring Standards](rules/rule_authoring_standards.md) |
| Skill & Rule A/B Benchmark | [XMC Trajectory Comparison Skill](skills/xmc_trajectory_comparison/SKILL.md) |


---

## Modular Rules Index

### Core Engineering & Operational Workflows
- 📘 [Core Engineering Standards](rules/core_engineering.md): SRP, KISS, 20–30 line function limit, linear control flow, default immutability, centralized shared helpers, baseline test fixtures, high-fidelity test doubles, strict coverage, PR sizing thresholds, and post-implementation reflection rules.
- 📐 [Architectural Contracts & Invariants](rules/architectural_contracts.md): Core platform invariants including the Non-Restarting JobSets Contract (`MaxRestarts: 0`, `PodSpec.RestartPolicy: Never` for primary containers), agent halt triggers, and WorkUnit execution lifecycle boundaries.
- 🛠️ [Skills & Automation Guidelines](rules/skills_and_automation.md): Extracting complex logic into testable Python scripts, applying DRY/KISS to skills, preventing context creep, and delegating work to subagents.
- 🪜 [Rule Authoring Standards](rules/rule_authoring_standards.md): Root-cause routing via the seven-rung control ladder (Eliminate, Automate, Tool, Route, Document, Annotate, Discard), the mandatory Lint-First Test, the causal chain requirement gating discard, the invariant-vs-procedure applicability router, evidence-based boundary derivation (positive/negative/undecided sets, two-positive minimum, complement-aware search), the merge-first rule budget, and the documented deviation protocol.
- 🚀 [XMC Release Skill](skills/xmc_release/SKILL.md): End-to-end release orchestration playbook coordinating deployment to nightly, automated E2E and visual snapshot verification, abort gates, structured release notes synthesis, and confirmed publishing.

### GitHub & PR Workflows
- 🚀 [GitHub Authoring & PR Creation](rules/github_authoring_and_prs.md): Git branch prefixes (`feat/`, `fix/`, `refactor/`, `docs/`, `test/`, `chore/`, `koder/`), mandatory session initialization branch check policy, Conventional Commit standards, PR sizing gates, PR title & body formatting rules, mandatory assumptions disclosure in PR bodies, mandatory tags (`TAG=agy`), pre-commit linter policy, forbidden raw log dumps, and CI wait buffer policy.
- 🔀 [XMC PR Split Verification Skill](skills/xmc_pr_split_verification/SKILL.md): Sizing threshold evaluation, SRP verification subagent rubric, vertical slice decomposition, dead code prevention, and `gh-stack` execution plans.
- 📦 [XMC Create PR Skill](skills/xmc_create_pr/SKILL.md): Playbook for checking branch format, running pre-commit hooks, compiling the mandatory assumptions disclosure, formatting PR body templates, updating PR descriptions via helper script, managing CI wait buffers, and enforcing strict user confirmation gates for Draft PR creation and Open conversion.
- ⚡ [XMC Feature Development Skill](skills/xmc_feature_development/SKILL.md): Playbook for requirements scoping, clean code design, running affected test matrices, measuring Bazel coverage, and pre-commit verification.
- 📋 [Code Review & Comment Guidelines](rules/pr_review_guidelines.md): AI code review checklists (SRP, KISS, immutability, DI testing, test coverage, doc sync), assumptions disclosure audits, review feedback standards, and mandatory PR comment resolution rules.

### Language-Specific Rules
- 🐹 [Go (Golang)](rules/languages/golang.md): Idiomatic Go style, clean interfaces, lowercase error wrapping (`%w`), concurrency safety (`context.Context`, mutexes), table-driven testing with baseline fixtures (`proto.Modify`), and centralized test utilities.
- 🟦 [TypeScript & Modern Angular](rules/languages/typescript_javascript.md): Prefer TypeScript over JavaScript, TypeScript strictness, modern Angular signals and control flow (`@if`, `@for`, `@switch`), state immutability, declarative styling maps, and DI polymorphism.
- 🐍 [Python](rules/languages/python.md): PEP 8 Python style guide, Python 3.14+ version enforcement, standard library preference, explicit public API type hints, docstrings, `unittest` framework, Python tooling over shell scripts, and local virtual environment requirements.
- 🐚 [Shell Scripting](rules/languages/shell.md): Minimal shell script scope (`set -euo pipefail`), double-quoting variables with braces (`"${VAR}"`), restricting Shell to thin entrypoints while deferring logic to Python.
- 📝 [Markdown Documentation](rules/languages/markdown.md): GFM formatting, heading hierarchy, clickable relative links, accurate contract representation, and synchronized documentation updates.
