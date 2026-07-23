---
name: XMC Iterative Reflection
description: Audits active session trajectory to identify friction points, rule gaps, and token inefficiencies.
---

# Iterative Reflection & Meta-Learning Workflow

Provides a retrospective workflow to evaluate session thought history and tool execution trajectory.

## 1. Trigger Criteria
Run **only** for:
- Explicit user prompt for reflection/retrospective.
- Major multi-step feature completion.
- High PR review comment volume (>3-5 comments).

Do NOT trigger for routine minor edits.

## 2. 4-Pillar Trajectory Audit
Audit session trajectory across 4 dimensions:
1. **Tooling & Automation Gaps**: Identify inline shell logic to extract to `tools/`.
2. **Rule & Guideline Gaps**: Identify missing rules in `.agents/rules/`.
3. **Skill & Workflow Gaps**: Identify missing playbooks in `.agents/skills/`.
4. **Token & Subagent Efficiency**: Identify missed subagent delegation opportunities.

Refer to `references/audit_pillars.md` for pragmatism filters and PR creation safety rules.
