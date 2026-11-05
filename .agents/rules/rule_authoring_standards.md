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

### Trace Before Routing

Before selecting **any** rung, trace the causal chain from the observed symptom back to its origin
and record where it terminates. Inspect **both** the definition site and the consumption site: a
symbol can be perfectly well declared and still be unresolvable at the point of use because of an
`Any` annotation, a dynamic attribute lookup, or a value obtained through a private field.

Tracing is a **diagnosis** step, not a control. Looking only at the definition site and missing a
call-site `Any` is how an agent falls past rungs 1–2 and writes a superficial prose rule about the
symptom; looking only at the call site and calling the local fix "rung 1" is how it skips the lint
gate that stops the next caller from repeating the pattern.

### The Control Ladder

Rungs are ordered by **enforcement strength**. Select the strongest applicable rung; rungs compose.

| # | Control | Destination | Applies when |
| :--- | :--- | :--- | :--- |
| 1 | **Eliminate** | Shared API, SDK, or type-definition fix, as a separate PR | A shared surface made the mistake possible or likely, and changing it eliminates the hazard **for all callers** |
| 2 | **Automate** | Lint rule or CI gate | The invariant is mechanically checkable |
| 3 | **Tool** | Helper script in `tools/` | A multi-step procedure needs automating |
| 4 | **Route** | [`AGENTS.md`](../AGENTS.md) routing matrices, or rule relocation | The rule already existed but was never found |
| 5 | **Document** | New prose rule in `.agents/rules/` or skill in `.agents/skills/` | A genuine, generalizable knowledge gap remains |
| 6 | **Annotate** | Scoped suppression with a stated reason | A governed control exists and this file legitimately deviates |
| 7 | **Discard** | No change | The traced chain terminates outside our control, or the root cause inside our control is a non-recurring, low-ROI one-off |

> [!IMPORTANT]
> **Fixing a single call site is not rung 1.** Patching the file in front of you repairs the
> incident; it does nothing for the next caller. Because rung 1 outranks rung 2, classifying a local
> call-site edit (`stub: Any`, a missing annotation, a one-file construction mistake) as rung 1 lets
> an agent declare the strongest control applied and stop — bypassing the rung-2 lint or typecheck
> gate (`ANN401`, `SLF001`, `mypy`) that actually prevents recurrence. Fix the call site in the
> active PR as ordinary review work, and route the **recurring pattern** to rung 2.

> [!IMPORTANT]
> Prose rules are a **last-resort control**. Every rule permanently consumes the attention budget of
> every future agent, whereas a lint rule costs nothing at steady state. Reaching rung 5 requires
> explicitly recording why rungs 1–4 do not apply.

Rung 7 is valid in two cases, both gated on the trace having been done first: **(a)** the chain
terminates outside our control, or **(b)** the root cause inside our control is verified to be a
non-recurring, low-ROI one-off. A correct verdict on the surface claim does not license skipping the
trace to reach either exit — confirming that code behaves correctly says nothing about whether our
code made it *look* incorrect, nor about whether the same pattern appears elsewhere.

### Non-Ladder Branch: External Tooling False Positives

When an automated review bot flags code that is in fact correct, two things must be established, not
one:

1. **The claim is false.** Verify against the source tree.
2. **Why the tool was misled.** Apply [Trace Before Routing](#trace-before-routing) above. If a
   shared surface or missing lint/typecheck gate under our control made the code ambiguous to static
   analysis, that ambiguity is a **rung 1 or rung 2** finding in its own right, even though the bot's
   claim was wrong.

Only when the chain terminates outside our control does this route to bot configuration or a reply
on the review thread. Never encode a workaround for a tool's misunderstanding as a rule — and note
that a natural-language comment addressed to an automated reader is such a workaround, not a fix.

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
| TypeScript / UI | `cmd/ui/eslint.config.js`, prettier, `tools/lint/check_file_naming.py` (UI template/test and `.github/scripts/` test naming) |
| Bazel | buildifier |
| Terraform | [`.tflint.hcl`](../../.tflint.hcl) |
| Agent docs (`.agents/**/*.md`, `AGENTS.md`) | `tools/lint/check_agent_doc_paths.py` (path references must exist) |

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
- **Scope by evidence, not by incident**: derive the rule's scope using
  [Evidence-Based Boundary Derivation](#5-evidence-based-boundary-derivation) below. A rule scoped to
  the single file that triggered it is almost always wrong.

---

## 5. Evidence-Based Boundary Derivation

Applies whenever a control's boundary must be drawn — a rung-2 lint predicate as much as a rung-5 prose
rule. An over-broad predicate fires on legitimate code and trains the suppression reflex; an over-narrow
one silently misses the class it was written for. The scope MUST be derived from enumerated files, never
from the file that happened to trigger it. Partition the evidence into three sets:

| Set | Contents | Produced by |
| :--- | :--- | :--- |
| **Positive** | Files the rule must govern | Generalizer — broadens scope and proves reach |
| **Negative** | Files the rule must *not* govern | Falsifier — hunts counterexamples |
| **Undecided** | Files whose existing treatment contradicts itself | Either; forces escalation |

The rule is then the **narrowest predicate that covers the whole positive set and excludes the whole
negative set**.

### Requirements

- **Two independent positives minimum.** The positive set MUST contain at least two files outside the
  originating incident. A finding that cannot clear this bar is not generalizable: route it to rung 6
  if a governing control exists, otherwise rung 7.
- **Complement-aware search (MUST).** Searching only for files matching the proposed rule will
  confirm it and stop. Deliberately search the complement instead, and answer both questions
  explicitly:
  - Which files does the predicate **exclude that it arguably should cover**?
  - Which files does it **include that it must not**?
- **Contradictory evidence escalates.** When two comparable files receive opposite treatment, no
  consistent rule exists to be learned. Surface the contradiction as a question to the user rather
  than picking a side silently. Rules synthesized from contradictory evidence are exactly the ones
  reviewers reject, because the reviewer knows the counterexample. Once resolved, each undecided file
  moves into the positive or negative set, or becomes a rung-6 documented deviation.
- **Prefer semantic predicates over path globs.** A glob is easy to write and easy to get wrong: it
  tends to encode the path of the triggering file rather than the property that matters. If a glob is
  used, verify it actually covers every member of the positive set.

### Worked Example

The `examples/` unit-test convention in
[Core Engineering Standards](core_engineering.md#5-strict-testing-coverage--pr-granularity)
illustrates the target shape. Scoped to `examples/*/launch.py` it would have covered only 12 of some
25 comparable scripts and missed `examples/xdash_dashboard/create_dashboard.py`; widened to all of
`examples/` it would have wrongly captured the legitimately tested
`examples/chart_artifact_job/training_metrics.py`. The rule as written names both sides — the
entrypoint, launch, and container workload scripts it exempts, and the shared helper modules it does
not — so a reader can place a new file without guessing.

---

## 6. Rung 6: Documented Deviation Protocol

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
