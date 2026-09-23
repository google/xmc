# Rule Authoring Standards

This document governs how agents turn an observed friction point into a durable change. It applies
whenever [Iterative Reflection](../skills/xmc_iterative_reflection/SKILL.md) or a PR review
retrospective produces a candidate rule, tool, or guideline.

Its purpose is to stop two recurring failure modes: routing a finding into a prose rule when a
stronger control was available, and scoping a rule to the single file that happened to trigger it.

---

## 1. Root-Cause Routing: The Control Ladder

Every finding MUST be routed to a destination before any fix is drafted. Detecting *what went
wrong* does not determine *what should change* — choose the destination deliberately.

Rungs are ordered by **enforcement strength**. Select the strongest applicable rung; rungs compose.

| # | Control | Destination | Applies when |
| :--- | :--- | :--- | :--- |
| 1 | **Eliminate** | Product code or API fix, as a separate PR | An API gap or poor ergonomic *caused* the mistake |
| 2 | **Automate** | Lint rule or CI gate | The invariant is mechanically checkable |
| 3 | **Tool** | Helper script in `tools/` | A multi-step procedure needs automating |
| 4 | **Route** | [`AGENTS.md`](../AGENTS.md) routing matrices, or rule relocation | The rule already existed but was never found |
| 5 | **Document** | New prose rule in `.agents/rules/` or skill in `.agents/skills/` | A genuine, generalizable knowledge gap remains |
| 6 | **Annotate** | Scoped suppression with a stated reason | A governed control exists and this file legitimately deviates |
| 7 | **Discard** | No change | One-off, low ROI, or an external tool was simply wrong |

> [!IMPORTANT]
> Prose rules are a **last-resort control**. Every rule permanently consumes the attention budget of
> every future agent, whereas a lint rule costs nothing at steady state. Reaching rung 5 requires
> explicitly recording why rungs 1–4 do not apply.

### Non-Ladder Branch: External Tooling False Positives

When an automated review bot flags code that is in fact correct, the root cause is the bot, not the
codebase. Route to bot configuration or a reply on the review thread. Never encode a workaround for
a tool's misunderstanding as a rule.

---

## 2. Applicability Router: Invariants vs. Procedures

Rungs 2 and 3 are usually not substitutes. Classify the finding by **shape** before comparing
enforcement strength:

| Finding shape | Rung | Why the alternative fails |
| :--- | :--- | :--- |
| **Invariant / prohibition** — "X must never appear", "Y must have a sibling Z" | 2 | A helper script cannot prevent anyone from doing the thing |
| **Procedure** — ordered multi-step sequence, external API calls, orchestration | 3 | A linter cannot express "run these five steps in this order" |
| **Both** | 2 and 3 | The tool makes the right path easy; the linter makes deviation impossible |

Where both genuinely apply to the same finding, the gate wins.

---

## 3. Rung 2: The Lint-First Test

> [!IMPORTANT]
> Before proposing any prose rule, ask: *is this mechanically checkable from the source tree?* If
> yes, it MUST be implemented as a lint rule. A prose rule is not an acceptable substitute for a
> lintable invariant.

Work the sub-ladder cheapest-first:

- **2a — Enable an existing rule.** One entry plus a rationale comment, matching the established
  convention in [`pyproject.toml`](../../pyproject.toml) and [`.golangci.yml`](../../.golangci.yml).
  Always run a feasibility check first: enabling a rule repository-wide surfaces pre-existing
  violations that need either a cleanup sweep or scoped ignores.
- **2b — Express the constraint as configuration.** For example a banned API entry carrying a custom
  remediation message, or an ignore-words list.
- **2c — Write a custom check.** Precedent: `tools/lint/check_go_determinism`.
  **Cost guard**: if 2a and 2b are unavailable *and* the check would require more than a
  straightforward AST or regex pass, or would surface a large violation backlog, 2c MAY be deferred
  to rung 3 or 5 with the rationale recorded. A misfiring bespoke linter trains reflexive
  suppression, which corrodes rung 6. Documented deferral is acceptable; silent skipping is not.

Destinations by language, all reachable through `.githooks/pre-commit` and its `--check` CI mode:

| Language | Destination |
| :--- | :--- |
| Python | [`pyproject.toml`](../../pyproject.toml) ruff `select` and banned-api config, mypy |
| Go | [`.golangci.yml`](../../.golangci.yml), `nogo_config.json`, custom checks in `tools/lint/` |
| TypeScript / UI | `cmd/ui/eslint.config.js`, prettier |
| Bazel | buildifier |
| Terraform | [`.tflint.hcl`](../../.tflint.hcl) |

---

## 4. Rung 5: Writing a Prose Rule Responsibly

When a finding legitimately reaches rung 5:

- **Record the routing rationale**: one line stating why rungs 1–4 do not apply.
- **Merge before appending**: attempt to amend an existing bullet that covers adjacent ground before
  adding a new one. If no existing bullet fits, state that explicitly.
- **One canonical location**: place the rule in exactly one file and cross-link from any other
  relevant document, per
  [Skills & Automation Guidelines](skills_and_automation.md#2-dry--kiss-in-skill--rule-design).
  Never duplicate rule text across files.
- **Scope by evidence, not by incident**: a rule scoped to the single file that triggered it is
  almost always wrong. Enumerate the files the rule governs and the files it must not govern before
  fixing its wording.

---

## 5. Rung 6: Documented Deviation Protocol

Rung 6 covers a specific case: **a governing control exists, and this file legitimately departs from
it.** It is not a home for findings that are merely too small to generalize — those are rung 7.

- **Precondition**: a rung-2 lint rule or rung-5 prose rule MUST already exist, or be created in the
  same change. A deviation with nothing to deviate from is rung 7.
- **Configuration-scoped**: per-file ignore entries or linter exclusion lists, each carrying a
  comment stating the reason. See the per-file-ignores block in
  [`pyproject.toml`](../../pyproject.toml) for the established pattern.
- **Code-scoped**: an inline suppression at the deviation site that names both the rule and the
  reason, for example `# noqa: RULE - reason` or `//nolint:linter // reason`.
- **Anti-pattern**: any suppression without a stated reason.
