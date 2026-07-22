# Skills & Automation Guidelines

This document provides standards for skill architecture, workflow automation, and context preservation using subagents.

---

## 1. Extracting Complex Logic to Executable Python Scripts
- **Executable Scripts over Inline Commands**: Never embed multi-step parsing logic, string manipulation, or inline shell code (e.g., `python3 -c "..."`) inside Markdown skill definitions or shell invocations. Always write Python code to standalone `.py` script files via `write_to_file` or rely on pre-written tools in `tools/`.
- **Predictable & Testable Execution**: Extract complex logic into dedicated Python scripts located in `tools/` or skill-specific script directories.
- **Automated Verification**: Accompany every extracted script with unit tests (e.g., `tool_name_test.py` utilizing `unittest`) to verify correctness, parameter validation, and exception handling without relying on manual trial-and-error.

## 2. DRY & KISS in Skill & Rule Design
- **Single Source of Truth for Rules & Skills (No Duplication)**: Every engineering policy, coding standard, tool requirement, or workflow rule MUST exist in exactly ONE canonical document under `.agents/rules/` or `.agents/skills/`. NEVER duplicate identical text, bullet points, or guidelines across multiple rule files or skills. When referencing an existing rule, link to the canonical document instead of copy-pasting text.
- **Shared Utilities**: When multiple skills or automation playbooks share common setup, verification, or deployment steps, extract the common execution into shared Python modules or reusable scripts.
- **Focused Skill Scope**: Each skill must fulfill a single clear capability. Avoid building monolithic, overly broad "everything-in-one" skill markdown definitions.
- **Modular Decomposition**: Decompose expansive operational playbooks into discrete sub-skills or composable scripts.

## 3. Preventing Context Creep
- **Concise Skill Instructions**: Keep `SKILL.md` instructions brief, direct, and high-level. Move reference materials, detailed API specifications, or schema definitions into separate files under `references/` or `resources/` subdirectories within the skill folder.
- **On-Demand Context Loading**: Agents should load reference documentation or sub-files only when performing tasks that explicitly require them, preventing unnecessary context bloat in prompt memory.
- **Modular Rule Splitting**: Keep rules categorized into narrow, purpose-built files under `.agents/rules/` to ensure agents read only the context relevant to their current execution phase.

## 4. Subagent Delegation Guidelines
- **Preventing Main Context Exhaustion**: Delegate context-heavy tasks—such as deep codebase exploration, parsing large test logs, multi-file searches, or running extensive build iterations—to worker subagents.
- **Tightly Scoped Subagent Prompts**: Provide worker subagents with specific file scopes, clear execution bounds, and explicit expected output requirements.
- **Structured Synthesis Reporting**: Subagents must analyze intermediate outputs and communicate clean, structured summaries and actionable conclusions back to the parent agent via structured messaging.

