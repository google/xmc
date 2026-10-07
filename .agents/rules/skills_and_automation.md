# Skills & Automation Guidelines

This document provides standards for skill architecture, workflow automation, and context preservation using subagents.

---

## 1. Extracting Complex Logic to Executable Python Scripts
- **Executable Scripts over Inline Commands**: Never embed multi-step parsing logic, string manipulation, or inline shell code (e.g., `python3 -c "..."`) inside Markdown skill definitions or shell invocations. Always write Python code to standalone `.py` script files via `write_to_file` or rely on pre-written tools in `tools/`.
- **Predictable & Testable Execution**: Extract complex logic into dedicated Python scripts located in `tools/` or skill-specific script directories.
- **Multi-Phase Polling & Workflow Orchestration in Dedicated Python Helpers**: Multi-step remote CI polling, GitHub API orchestration, JSON parsing, or condition evaluation MUST NOT be implemented via inline bash loops with `sleep` or fragile jq filters in skill definitions. Encapsulate them into dedicated Python CLI tools in `tools/` with hermetic unit tests. Inline bash in skill definitions is restricted strictly to simple single-line invocations, environment variable definitions, or piping preflight commands.
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
  - **Exit Code 1**: Handled Error. Parse `"message"` and `"instruction"` fields to remediate inputs. CLI linter and ratchet gates invoked in `.githooks/pre-commit` with `stdout` redirected (`>/dev/null`, such as `tools/lint/check_legacy_imports.py` and `tools/nightly_tests/check_examples_coverage.py`) MUST emit failure JSON payloads and diagnostics to `sys.stderr` so error details remain visible in hook and CI output.
  - **Exit Code 2**: Unhandled Error. Inspect `"message"` and `"traceback"` fields to determine root cause.
- **Repository-Scanning Gate Invariants**: Custom linter and coverage gates that scan repository directories or enforce allowlist/exemption registries MUST: (1) explicitly verify that the target root directory exists before iterating (failing fast on invalid `--repo-root` paths rather than silently reporting zero violations), and (2) cross-check all allowlist, baseline, or exemption keys against existing filesystem entries so stale exemptions for renamed or deleted paths fail the gate.
- **Shell Command & Inline Code Execution Safety**: Avoid double-quoted inline shell code invocations (`python3 -c "..."`) when passing Markdown, JSON, or multiline parameters. Execute standalone `.py` script files or pass explicit CLI flags.
- **Localized Directory Scope (Strict Path Lock Policy)**: When executing operations or inspecting workspace state (e.g. `./ctk-out/`), agents MUST restrict searches strictly to the immediate working directory. Global recursive searches across parent directories, home folders, or system root (`find_by_name` across `~` or `/`) are strictly prohibited.

## 6. Context-Aware Execution & Test Scope Constraints
- **Conditional Test Suite Execution**: Agents MUST run unit test suites (`python3 tools/*_test.py` or `./tools/run_affected_tests.py`) ONLY when writing or modifying code, refactoring packages, or editing executable scripts.
- **Prohibition on Unrequested Tests**: For read-only inspection, document summaries, rule/skill editing, PR description generation, or retrospective analysis tasks, agents MUST NOT run test suites or execute unrequested tool chains that add unnecessary turn count and token overhead.


## 7. Helper Script Maintenance & Deduplication
- **Centralized Helper Library**: Maintain common python automation helpers in `tools/` with standard library dependencies to eliminate duplicate code blocks across skills and subagents.
- **Standard Library Preference**: Prefer Python standard library modules (`os`, `sys`, `json`, `subprocess`, `urllib.request`) over external third-party packages to guarantee zero-dependency execution in all environments.

## 8. Subagent Delegation Policy for Context Preservation
- **Offload Heavy Traversal & Analysis**: When a task involves reading multiple log files, running heavy test matrices, or performing broad codebase searches, delegate execution to worker subagents (`research-google` or `self`).
- **Prevent Main Context Bloat**: Do NOT dump raw test logs or intermediate shell command outputs into the primary agent's context window. Remote CI watchers and test runners must parse and summarize job outcomes into structured JSON (e.g. `failed_jobs`), and worker subagents must summarize key metrics and actionable recommendations in their return messages.

## 9. Workflow Orchestration & Remote Automation Invariants
- **Strict Target Commit Pinning**: Skills and automation scripts that orchestrate, verify, or validate builds, deployments, E2E test runs, or releases MUST explicitly pin operations and target comparisons to the canonical remote target (e.g. `TARGET_SHA=$(git rev-parse origin/main)`). Never default to `HEAD` or local uncommitted working tree state when executing or verifying release, staging, or deployment workflows.
- **Asynchronous Run Correlation & Polling Invariants**: When polling for newly triggered remote CI runs (e.g. GitHub Actions, Borg jobs), helpers MUST:
  1. Record dispatch timestamp $t_0$ and filter strictly on `createdAt >= t_0`.
  2. Require matching **both** timestamp and commit SHA (`headSha.startswith(target_sha)`). Never use loose `or` conditions in polling loops.
  3. Distinguish distinct execution conclusions (`success`, `failure`, `cancelled`, `sha_mismatch`) with dedicated exit codes. Report concurrency cancellation specifically if a newer run in the concurrency group supersedes the active run.
- **Non-Mutating Preflight Validation (`--dry-run`) Before Confirmation Gates**: Before waiting on a mandatory confirmation gate for mutating, irreversible, or public operations (such as publishing a release or creating a git tag), skills and tools MUST run non-mutating preflight validation (`--dry-run`). This verifies parameter validity, commit SHA resolution, remote tag availability, and artifact existence *before* prompting the user for approval.
- **Prohibition on `--force` and Autonomous Tag Overwrites**: Skills and automation tools MUST NEVER use `--force` or overwrite existing git tags or releases autonomously. If a tag or release already exists locally or on the remote, abort immediately and consult the user.
- **Resumption State Loading & Persistence Idiom**: Persist multi-turn execution state, verification results, and generated reports using `${ARTIFACT_DIR:-/tmp}/<file>.<ext>` to ensure survivability across session restarts and CI artifact preservation. For the canonical bash fallback state-loading snippet (`jq -r '.target_sha // empty'`) and top-of-block variable placeholder consolidation (`VAR="<placeholder>"`), follow [Shell Scripting Guidelines](languages/shell.md#2-safety-options--robust-execution).
- **Single Source of Truth for Workflow Actions**: When a skill orchestrates an end-to-end pipeline (such as release orchestration), it MUST delegate discrete responsibilities (like release notes drafting, publishing, and draft-to-public promotion) to the canonical owning skill rather than reimplementing or duplicating the step.
- **Closed-Circuit Error Propagation (No `|| true`)**: Never append `|| true` to command invocations inside Markdown skill definitions. Allow non-zero exit codes to fail fast and bubble up to the agent for proper error inspection and recovery.
- **Explicit Heading Slug Anchors**: Cross-skill references MUST link to Markdown heading slug anchors (e.g. `[Mandatory Confirmation Gate](../other_skill/SKILL.md#6-mandatory-confirmation-gate--release-publishing)`) rather than volatile section numbers.
