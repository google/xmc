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

## 5. Helper Tool Execution Policy & Safety Constraints
- **Tool Source Code Isolation Policy**: Agents MUST NOT open, view, or read the Python source code of helper tools in `tools/` or skill script directories. Use `--help` to inspect tool parameters, inputs, and usage flags.
- **Structured Output & Error Code Parsing**: All helper tools output structured JSON data. Agents MUST check exit codes:
  - **Exit Code 0**: Success. Read `"result"` payload from stdout JSON.
  - **Exit Code 1**: Handled Error. Parse `"message"` and `"instruction"` fields to remediate inputs.
  - **Exit Code 2**: Unhandled Error. Inspect `"message"` and `"traceback"` fields to determine root cause.
- **Shell Command & Inline Code Execution Safety**: Avoid double-quoted inline shell code invocations (`python3 -c "..."`) when passing Markdown, JSON, or multiline parameters. Execute standalone `.py` script files or pass explicit CLI flags.
- **Localized Directory Scope (Strict Path Lock Policy)**: When executing operations or inspecting workspace state (e.g. `./ctk-out/`), agents MUST restrict searches strictly to the immediate working directory. Global recursive searches across parent directories, home folders, or system root (`find_by_name` across `~` or `/`) are strictly prohibited.
