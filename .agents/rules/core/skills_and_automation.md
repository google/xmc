# Skills & Automation Guidelines

This document defines standards for script extraction, DRY/KISS design, context bounds, and tool interactions.

---

## 1. Extracting Complex Logic to Executable Python Scripts
- **Executable Scripts over Inline Commands**: Never embed multi-step parsing logic, string manipulation, or inline shell code (`python3 -c "..."`) inside Markdown skill definitions or shell invocations. Write Python code to standalone `.py` script files in `tools/` or skill-specific script directories.
- **Automated Verification**: Accompany every extracted script with unit tests (e.g. `tool_name_test.py` utilizing `unittest`) to verify correctness and error handling.

## 2. DRY & KISS in Skill & Rule Design
- **Single Source of Truth**: Every engineering policy or workflow rule MUST exist in exactly ONE canonical document. Link to the canonical document instead of copy-pasting text.
- **Focused Skill Scope**: Each skill must fulfill a single clear capability. Keep `SKILL.md` under 80 lines and decompose complex steps into `references/`.

## 3. Context Creep & Subagent Delegation
- **On-Demand Context Loading**: Load reference documentation or sub-files only when performing tasks that explicitly require them.
- **Subagent Delegation**: Delegate context-heavy tasks—such as deep codebase searches, parsing large test logs, or running heavy build iterations—to worker subagents.
- **Structured Synthesis Reporting**: Subagents must synthesize raw outputs and communicate clean summaries back to the parent agent.
