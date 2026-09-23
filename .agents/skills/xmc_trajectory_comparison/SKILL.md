---
name: XMC Trajectory Comparison
description: Validates proposed skill or rule changes by executing concurrent A/B benchmark subagents on clean baseline vs candidate worktrees, comparing trajectories, and tearing down test resources.
---

# Skill & Rule Trajectory Comparison Benchmark

This skill provides an automated A/B benchmark workflow to evaluate proposed changes to rules (`.agents/rules/`) or skills (`.agents/skills/`) before committing them. It spawns dual worker subagents to execute a test prompt concurrently across both a clean baseline copy and your modified candidate worktree, compares execution trajectories and output quality, and tears down any temporary external resources.

```mermaid
graph TD
    A[Skill / Rule Edits in Worktree] --> B[1. Extract Structured Git Diff]
    B --> C[2. Main Agent Semantic Reasoning: Prompt Synthesis & Risk Classification]
    C --> M["2b. Derive 3-Scenario Matrix from Evidence Sets (Origin / Transfer / Negative)"]
    M --> D[3. User Confirmation & Teardown Plan Agreement]
    D -->|Approved| E1["Subagent Baseline: parent detached to baseline ref before spawn"]
    D -->|Approved| E2[Subagent Candidate: Workspace branch + modified worktree]
    E1 --> F1[Baseline Transcript Log & Outcome Artifacts]
    E2 --> F2[Candidate Transcript Log & Outcome Artifacts]
    F1 & F2 --> G[4. Run teardown_benchmark.py]
    G --> H[5. Run compare_trajectories.py --prompt ...]
    H --> I[6. Dual-Pillar Evaluation, Relevance Gate & 3-Scenario Verdict Matrix]
    I --> J[7. PR Integration & User Edit Gate]
```

---

## 1. Trigger Criteria

Run this skill when:
- **Iterative Reflection Validation**: You have formulated candidate rule, skill, or tool additions via [XMC Iterative Reflection](../xmc_iterative_reflection/SKILL.md) and want empirical A/B verification before opening a Draft PR.
- **Rule or Skill Authoring**: You have modified or added a rule (`.agents/rules/*.md`) or skill definition (`.agents/skills/*/SKILL.md`) in your working tree and want to verify its efficacy before submitting a PR.
- **Pre-Commit Verification**: You want to test if a rule update prevents past friction without causing new regressions, hallucinated output, or excessive turn count bloat.

---

## 2. Execution Workflow

### Phase 1: Structured Diff Extraction & Agent Synthesis
Run `tools/benchmark/analyze_diff_and_synthesize.py` to extract structured git diff data:

```bash
python3 tools/benchmark/analyze_diff_and_synthesize.py --target-dir .agents
```

The main agent parses the resulting JSON payload (`changed_files`, `added_lines`, `removed_lines`, `category`) and performs semantic reasoning to:
1. **Understand Intent**: Analyze the specific guideline, constraint, or workflow step added or modified.
2. **Synthesize Scenario Prompt** (if not provided by user): Draft a realistic test prompt designed to test whether an agent complies with the new constraint vs. unconstrained behavior.
3. **Assess Side-Effect Risk Tier**:
   - **Tier 1 (Read-Only / Local)**: No external side effects (local code generation, analysis).
   - **Tier 2 (Reversible Remote Artifacts)**: Creates PRs, remote test branches, or draft comments.
   - **Tier 3 (Infrastructure / High-Cost)**: Provisions GKE clusters, GCP resources, or cloud deployments.

> [!WARNING]
> **Ask for the artifact, not the method.** Neither the scenario prompt nor its deliverable spec may
> describe the behaviour the candidate change prescribes. Asking both arms to *"list every file the
> rule should govern and every file it should not"* while the candidate adds positive/negative evidence
> sets makes both arms produce those sets on instruction — the benchmark then measures
> instruction-following, and the arms tie no matter what the rule does. Request the workflow's normal
> output, and let the grading step check whether the prescribed behaviour appeared **unprompted**.
> Reconstruct search and tool behaviour from the transcripts instead of asking arms to self-report it,
> since a request to log searches is itself a nudge to search more.

### Phase 1b: Derive the Scenario Matrix

A single scenario drawn from the incident that motivated the change grades the candidate on its own
training example. It can establish that the change works where it was written, and nothing else — it
is blind to a predicate that is too narrow to reach the rest of its class, and blind to one broad
enough to fire on code it should leave alone.

Run three scenarios instead. They are sourced from the evidence sets that
[Evidence-Based Boundary Derivation](../../rules/rule_authoring_standards.md#5-evidence-based-boundary-derivation)
has already produced, so this costs no additional analysis:

| Scenario | Drawn from | The candidate must… | A failure detects |
| :--- | :--- | :--- | :--- |
| **Origin** | the originating incident | remove the observed friction | the change is ineffective |
| **Held-out transfer** | positive set, excluding the origin | behave the same way on a *different* member | **under-generalization** |
| **Negative regression** | negative set | **not** fire, or exempt the file explicitly | **over-generalization** |

- **Four arms, not six.** Origin needs baseline *and* candidate, to establish both that the friction
  is real and that the change removes it. Transfer and negative are candidate-only: they are graded
  against pre-registered expectations, so a baseline arm would add cost without adding information.
- **Transfer is always available.** The two-positive minimum guarantees a held-out member exists.
  Negative regression is mandatory whenever the negative set is non-empty; an empty negative set on a
  non-trivial change is itself a finding worth surfacing, because a predicate that excludes nothing
  was never falsified.
- **Pre-register each pass predicate before launching.** State it as a mechanical assertion over the
  resulting worktree or transcript — *`examples/xdash_dashboard/create_dashboard_test.py` MUST
  exist* — never as a judgement call such as *"the agent should handle dashboards well"*. A predicate
  written after reading the transcripts will be fitted to them.

> [!NOTE]
> A finding routed to a rung that needs no boundary has no evidence sets to draw on, and benchmarks
> with the origin scenario alone. Record that explicitly in the report rather than leaving the two
> missing scenarios unexplained.

### Phase 2: Risk Tier & Teardown Agreement
If the task involves Tier 2 or Tier 3 side effects, present the teardown contract to the user before launching subagents:
> *"Benchmark will test updates to `XMC Create PR`. Side Effects: Will create 2 temporary test PRs titled `[BENCHMARK TEST]` on remote. Post-benchmark cleanup will execute `gh pr close <PR_URL> --delete-branch`. Proceed with benchmark? (Y/n)"*

### Phase 3: Dual Concurrent Subagent Execution

> [!IMPORTANT]
> **A subagent's injected rules must match its own disk state.** `.agents/` content is injected into a
> subagent's system prompt at spawn time, sourced from the **parent** worktree. A `git reset` issued
> *inside* the subagent changes its disk but not its already-injected prompt, so a baseline arm spawned
> from a candidate parent silently receives a summary of the very change under test. Never rely on an
> in-subagent reset to produce a clean baseline.

1. **Baseline Subagent** — park the parent worktree on the baseline commit *before* spawning. The
   working tree MUST be clean first: `git switch --detach` carries uncommitted changes across, so
   unstaged candidate edits would ride into the baseline arm — the very leak this procedure exists to
   prevent. Commit them to the candidate branch, or stash them, before detaching.

   ```bash
   test -z "$(git status --porcelain)" || { echo "dirty tree: commit or stash first"; exit 1; }
   git switch --detach <baseline_ref>   # parent worktree now matches the baseline
   # → invoke_subagent for the baseline arm here, with Workspace: "branch"
   git switch -                         # restore as soon as invoke_subagent returns
   ```

   - `Workspace`: `"branch"` — the arm's branch is cut from the detached parent, so it starts clean and needs no reset.
   - `Role`: `"Baseline Subagent"`
   - `Prompt`: execute the target test prompt. Do **not** instruct a reset.
2. **Candidate Subagent** — spawn after the parent is restored:
   - `Workspace`: `"branch"`
   - `Role`: `"Candidate Subagent"`
   - `Prompt`: Execute the target test prompt directly within the modified worktree context, adhering strictly to context-aware execution bounds (do NOT run unrequested test suites for read-only or document summary prompts).

The two arms are spawned seconds apart but still execute concurrently. Verify the isolation held before
trusting the results: `git -C <baseline_arm_worktree> log --oneline -n 1` must report the baseline ref.

**Contamination self-check (MUST).** Instruct every arm to report, as its first action, whether the
`.agents/AGENTS.md` text on disk matches the version pre-loaded into its system prompt, and to treat the
on-disk file as authoritative if they differ. If an arm reports a mismatch, the run is void **for every
criterion the leaked text names** — re-run that arm. Criteria the leaked text does not mention remain
valid, so record which is which instead of discarding the whole run.

### Phase 4: Resource Teardown & Manifest Cleanup
After both subagents complete, run `tools/benchmark/teardown_benchmark.py` to clean up any registered PRs, branches, or temporary manifest items:

```bash
python3 tools/benchmark/teardown_benchmark.py --manifest /tmp/benchmark_manifest_<id>.json
```

### Phase 5: Dual-Pillar Evaluation & Relevance Gate
Pass the `transcript.jsonl` log file paths from both subagents to `tools/benchmark/compare_trajectories.py`:

```bash
python3 tools/benchmark/compare_trajectories.py <baseline_transcript_path> <candidate_transcript_path> --prompt "<test_prompt>" -o /tmp/trajectory_report.md
```

The main agent reviews the output and populates the scorecard using the **Prompt Relevance Gate**:

1. **Target Test Prompt & Context Footprint**: Include prompt text and context window byte/token percentage.
2. **Prompt Relevance Gate**: Ask: *"Were extra turns or tool executions (e.g. running unit tests, extra searches) directly relevant to fulfilling the test prompt?"*
   - **If Relevant** (e.g. prompt modified code): Count test execution as a benefit (`🟢 Candidate`).
   - **If Irrelevant** (e.g. prompt was a documentation summary): Flag extra turns as 🔴 **Unnecessary / Over-Zealous Overhead**, and mark Functional Correctness as `N/A (Irrelevant)`.
3. **Pillar 1: Trajectory Telemetry**: Evaluate turn deltas, tool usage, error steps, and provide qualitative LLM reasoning on metric increases.
4. **Pillar 2: Outcome Quality Evaluation**: Side-by-side output previews & LLM-as-a-Judge Scorecard (Rule adherence, functional correctness, completeness).
5. **🏆 Final Benchmark Verdict**: derive it from the three scenarios rather than from an overall
   impression. A `✅` means that scenario's **pre-registered** predicate held.

   | Origin | Transfer | Negative | Verdict |
   | :---: | :---: | :---: | :--- |
   | ✅ | ✅ | ✅ | **SHIP** |
   | ✅ | — | — | **SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED)** — when no boundary was needed (no evidence sets), or when transfer/negative was omitted |
   | ✅ | ✅ | ❌ | **OVER-GENERALIZED** — narrow the predicate, or add an explicit rung-6 exception for the negative member |
   | ✅ | ❌ | ✅ | **UNDER-GENERALIZED** — widen the predicate and re-derive the boundary |
   | ✅ | ❌ | ❌ | **WRONG PREDICATE** — return to boundary derivation; the scope is mis-shaped, not merely mis-sized |
   | ❌ | — | — | **REJECTED** — ineffective at the origin, or the finding was misrouted on the control ladder |

   Two qualifiers attach on top of the matrix:
   - **`SHIP WITH RESERVATIONS (NEEDS EFFICIENCY TUNING)`** when the candidate obeyed the change but
     ran tools irrelevant to the prompt, per the Relevance Gate above.
   - A verdict is **void for every criterion named by leaked text** under the Phase 3 contamination
     self-check, whatever the matrix says. Report the voided criteria instead of the verdict.

Save the benchmark markdown snippet to `/tmp/benchmark_report_<branch_name>.md`.

---

## Phase 6: PR Description Integration & User Edit Gate

After completing the Dual-Pillar report:

### Scenario A: PR Already Exists for Active Branch
1. Check if an open PR exists using `gh pr view --json number,url,body`.
2. If found, prepare `updated_pr_body.md` by appending/updating the `## 🧪 Trajectory & Outcome Benchmark Results` section in the PR description.
3. **User Edit Gate**: Present the updated PR body preview to the user and pause:
   > *"Existing PR #<number> detected. I have prepared the updated PR description in `updated_pr_body.md` with the new benchmark findings.*
   > 
   > *[Markdown Preview]*
   > 
   > *Feel free to edit `updated_pr_body.md` directly. Proceed with `./tools/pr_review/update_pr_description.py <number> --body-file updated_pr_body.md`? (Y/n)"*
4. Upon confirmation, execute `./tools/pr_review/update_pr_description.py <number> --body-file updated_pr_body.md`.

### Scenario B: No PR Created Yet
1. Save the benchmark artifact to `/tmp/benchmark_report_<branch_name>.md`.
2. When the user subsequently runs [XMC Create PR](../xmc_create_pr/SKILL.md), it will auto-detect the file, embed the benchmark block into `pr_body.md`, and present the draft to the user for confirmation/edits before opening the PR.
