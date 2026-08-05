---
name: XMC Iterative Reflection
description: Audits active session trajectory and thought history to identify friction points, rule gaps, missing helper tools, and token inefficiencies. Use when requested by the user, after major implementations, or when addressing PRs with high comment volume.
---

# Iterative Reflection & Meta-Learning Workflow

This skill provides a systematic retrospective workflow to evaluate an agent's recent thought history and tool execution trajectory. It identifies friction, inefficiencies, rule gaps, and missing scripts, and formulates non-destructive, high-ROI improvement proposals.

```mermaid
graph TD
    A[Trigger Event] -->|Major Implementation / High PR Comments / User Prompt| B[1. Inspect Trajectory Log]
    A -->|Minor Edit / Routine 1-Comment Fix| Stop[Do NOT Trigger Reflection]
    B --> C[2. Perform 4-Pillar Trajectory Audit]
    C --> D[3. Apply Pragmatism & Anti-Hallucination Filter]
    D -->|Recurrent Friction Found| E[Formulate High-ROI Proposals]
    D -->|Smooth Trajectory / One-off Friction| F[Report: No Action Needed]
    E & F --> G[4. Present Review Report & Await Approval]
    G -->|Do NOT apply fixes automatically| End[User Decision]
```

> [!IMPORTANT]
> **Safety & Pragmatism Guardrails**:
> 1. **Do NOT Hallucinate Issues**: A valid and expected outcome of reflection is **"No optimizations recommended"**. Do not invent artificial problems for smooth or one-off tasks.
> 2. **Proposals Before Edits**: Never apply file modifications, rule updates, or script additions automatically. Always present proposals to the user for explicit review first.

---

## 1. Trigger Criteria

Run this skill **only** when one of the following conditions is met:
- **Explicit User Prompt**: The user asks for a retrospective, prompt audit, or performance reflection.
- **Major Task Completion**: Completion of a complex, multi-step feature or architectural refactor.
- **High PR Comment Volume**: Resolving a Pull Request with high review comment volume (>3-5 comments or multiple review iterations), indicating friction or missing rule clarity.

> [!CAUTION]
> **Do NOT Trigger For**: Routine minor edits, simple single-line fixes, or 1-comment PR updates. Avoid over-eager reflection that adds token bloat for simple tasks.

---

## 2. 4-Pillar Trajectory Audit

Evaluate session logs (`transcript.jsonl`) across four key dimensions:

### Missing Transcript Fallback Protocol
If `transcript.jsonl` for a conversation ID is unindexed or missing from local disk:
1. **Audit Git History**: Run `git log --all --grep="<CONV_ID>" --stat -p` to inspect committed changes, review comments, and PR metadata.
2. **Audit Workspace Worktrees & Reflog**: Inspect worktree paths under `~/.gemini/jetski/worktrees/xmc/` and check `git reflog --all`.
3. **Execute Helper Inspection**: Run `python3 tools/inspect_session.py --conv_id <CONV_ID>` to parse available git metadata and task logs cleanly without unconstrained filesystem traversals.

### Pillar 1: Tooling & Automation Gaps (Helper Scripts)
- **Signal**: Did the agent write complex shell logic, custom python one-liners, or manual regex filtering directly inside commands or chat responses?
- **Action**: Identify opportunities to extract procedural logic into testable Python scripts in `tools/` (per [skills_and_automation.md](../../rules/skills_and_automation.md#1-extracting-complex-logic-to-executable-python-scripts)).


### Pillar 2: Rule & Guideline Gaps (`.agents/rules/`)
- **Signal**: Did review comments or agent trajectory show wrong assumptions, path wandering, or style guideline violations?
- **Action**: Formulate exact rule additions in `.agents/rules/` to prevent future missteps.

### Pillar 3: Skill & Workflow Gaps (`.agents/skills/`)
- **Signal**: Was the overall task a recurring engineering playbook that lacked step-by-step guidance?
- **Action**: Propose a new skill definition (`SKILL.md`) outlining the standardized workflow.

### Pillar 4: Token & Subagent Efficiency
- **Signal**: Did the agent read massive files unnecessarily, dump raw logs into prompt memory, or fail to delegate heavy search tasks to worker subagents?
- **Action**: Recommend subagent delegation (e.g., `research-google` or `self`) or tool usage adjustments.

---

## 3. Pragmatism & Anti-Hallucination Filter

Before adding any item to the proposed enhancements list, pass it through these checks:

1. **Recurrence Check**: Is this friction likely to happen again in future tasks, or was it a unique, one-off edge case? (If one-off, omit it).
2. **ROI Check**: Will the proposed rule/tool save more agent turns and token overhead than the cost of maintaining it? (If low ROI, omit it).
3. **Zero-Friction Result**: If trajectory was smooth or friction was a one-off case, allow a pragmatic "No Action Needed" outcome.

---

## 4. Formulate Report & Await Approval

Structure findings into a clean report:

### 📊 Retrospective Summary
- **Primary Task**: Short summary of what was implemented or reviewed.
- **Trigger Reason**: Major Implementation / High PR Comment Volume / User Request.
- **Trajectory Health**: Smooth / Minor Friction / Significant Friction.
- **Outcome**: Actionable Enhancements Proposed OR No Action Needed (Trajectory Optimal / One-off).


### 💡 Proposed Enhancements (If Applicable)

| Category | Component | Current Friction | Proposed Fix / Enhancement | Impact |
| :--- | :--- | :--- | :--- | :--- |
| **Tool / Script** | `tools/...` | Repetitive manual parsing | Create python script `tools/parse_x.py` | Saves 3-5 turns |
| **Rule** | `.agents/rules/...` | Reviewer caught missing style | Add rule to `.agents/rules/...` | Prevents PR feedback |

End by presenting the report in markdown with clickable file links and awaiting user feedback before taking any action.

---

## 5. Draft PR Creation & Workspace Safety

If the user requests creating a separate Draft PR for approved rule or skill enhancements:

1. **Isolated Subagent Workspace (Preferred)**:
   - If subagent execution with workspace isolation is supported (`Workspace: "branch"` or `"share"`), launch a worker subagent to handle PR creation.
   - **Clean Head Checkout**: The subagent MUST start by fetching and creating its dedicated branch directly from clean `origin/main` (e.g., `git fetch origin && git checkout -b docs/rules-update-... origin/main`). This ensures the reflection PR contains strictly rule/tooling enhancements and never mixes scope with the parent agent's active feature branch.
   - The subagent commits `.agents/` or `tools/` enhancements, pushes to remote, and opens a Draft PR (`gh pr create --draft`).
   - The primary agent's active worktree remains 100% untouched and safe from branch switching conflicts.

2. **Single-Workspace Fallback (Safeguard)**:
   - If subagent workspace isolation is unavailable in the execution environment, **do NOT perform local Git branch manipulations, stashing, or multi-PR switches** in the active worktree.
   - Present the proposed changes directly in the chat or artifact summary for user review to prevent workspace corruption.

