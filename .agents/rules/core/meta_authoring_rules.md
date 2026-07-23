# Meta-Authoring Rules for Rules and Skills

This document establishes standards for creating, updating, and structuring rules and skills under `.agents/`.

---

## 1. Rule & Skill Authoring Guardrails
- **No Clickable Link Traps**: NEVER place clickable markdown links (e.g. `[SKILL.md](path)`) in master routing files like `AGENTS.md` or meta-rules for optional rules or skills. Clickable links trigger automatic file loading traps. Use backtick string paths (e.g. `.agents/skills/pr_workflow/address_pr_comment/SKILL.md`) instead.
- **80-Line Cap for `SKILL.md`**: All `SKILL.md` files MUST be streamlined to under **80 lines**. Move large lookup tables, target mapping matrices, CTK group details, and schema specs into a `references/` subdirectory within the skill folder.
- **References Subdirectory Usage**: Place detailed documentation, schemas, and lookup tables in `references/<filename>.md` and load them on-demand only when executing that specific phase.
- **Mandatory Trigger Definitions**: Every skill MUST declare clear prompt keywords and file touch triggers in `.agents/rules/triggers/workflow_triggers.yaml`.

## 2. Structural Hygiene & Immutability
- **Single Source of Truth**: Never duplicate rule definitions across multiple files. Maintain canonical rules in `.agents/rules/core/` and reference them by path.
- **Atomic File Refactoring**: When moving or splitting rules/skills, purge old flat files immediately to guarantee zero dead files or duplication in the repository.
