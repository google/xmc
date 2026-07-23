# Agent Router Index

Lightweight routing index for AI agent sessions. Universal standards are anchored in the core engineering Single Source of Truth (SSOT).

## Core Engineering Principles (SSOT)
All engineering operations, code generation, and reviews MUST strictly follow `.agents/rules/core/core_engineering.md`.
High-risk actions require the mandatory gatekeeper protocol in `.agents/rules/core/anti_yolo_policy.md`.

## Universal Skill Trigger Matrix
Match task intent or modified file paths to load specialized workflows dynamically. Use string paths (never clickable links):

| Category | Intent / Prompt Keywords | File Scope Trigger | Skill Path |
| :--- | :--- | :--- | :--- |
| **PR Workflow** | Address PR comment, PR review feedback | PR comments, PR diffs | `.agents/skills/pr_workflow/xmc_address_pr_comment/SKILL.md` |
| **PR Workflow** | Retrospective, audit trajectory, reflection | Post-PR / Session audit | `.agents/skills/pr_workflow/xmc_iterative_reflection/SKILL.md` |
| **Feature Dev** | Local test coverage, measure package coverage | `*_test.go`, `*_test.py`, `*.spec.ts` | `.agents/skills/feature_dev/xmc_local_test_coverage/SKILL.md` |
| **Infra Mgmt** | Deploy XMC, initial cluster setup | `deploy/ctk_v2/*`, `gcluster` | `.agents/skills/infra_management/xmc_deploy/SKILL.md` |
| **Infra Mgmt** | Upgrade XMC, redeploy latest branch | `deploy/ctk_v2/*`, `upgrade_dev_env.py` | `.agents/skills/infra_management/xmc_upgrade/SKILL.md` |
| **Infra Mgmt** | Clean up environment, teardown cluster | `ctk-out/*`, `cleanup_env.py` | `.agents/skills/infra_management/xmc_cleanup_environment/SKILL.md` |
| **Debugging** | RCA, analyze test failures, root cause | `blaze-testlogs/*`, bug reports | `.agents/skills/debugging/xmc_root_cause_analysis/SKILL.md` |

## Rule Architecture Index
- Core Rules: `.agents/rules/core/core_engineering.md`, `.agents/rules/core/anti_yolo_policy.md`, `.agents/rules/core/meta_authoring_rules.md`, `.agents/rules/core/skills_and_automation.md`
- Workflows: `.agents/rules/workflows/feature_dev.md`, `.agents/rules/workflows/pr_review_address.md`, `.agents/rules/workflows/infra_management.md`, `.agents/rules/workflows/bug_fixing_debugging.md`
- Languages: `.agents/rules/languages/golang.md`, `.agents/rules/languages/python.md`, `.agents/rules/languages/typescript.md`, `.agents/rules/languages/shell.md`, `.agents/rules/languages/markdown.md`
- Declarative Triggers: `.agents/rules/triggers/workflow_triggers.yaml`
