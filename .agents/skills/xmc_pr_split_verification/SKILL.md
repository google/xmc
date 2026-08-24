---
name: XMC PR Split Verification
description: Analyzes pull request diffs exceeding size thresholds (400 prod / 1000 total / 1000 docs) to enforce Single Responsibility Principle (SRP), evaluate vertical slice splitting feasibility, eliminate dead code, and plan gh-stack PR stacks.
---

# PR Sizing & Vertical Split Verification Playbook

This skill defines the multi-agent workflow for analyzing large diffs before Pull Request creation, ensuring that complex changes are decomposed into clean, vertical slices with zero dead code and high-fidelity test reachability.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [GitHub Authoring & PR Creation Standards](../../rules/github_authoring_and_prs.md), [Core Engineering Standards](../../rules/core_engineering.md), and [gh-stack Skill](conversation://skills/gh-stack).

---

## Architecture & Workflow Overview

```mermaid
graph TD
    A["Agent Prepares to Open / Publish PR"] --> B["Run tools/analyze_diff_size.py"]
    B --> C{"Thresholds Exceeded?<br>(Prod >= 400 | Total >= 1,000 | Docs >= 1,000)"}
    C -->|No| D["Proceed to Standard PR Creation Gate"]
    C -->|Yes| E["Spawn PR Split Verification Subagent"]
    
    subgraph Subagent Verification Rubric
        E --> F["1. SRP Audit: Single cohesive reason to change"]
        E --> G["2. Vertical Splitting Priority: End-to-end user flows"]
        E --> H["3. Dead Code Invariant: No orphaned functions / uncalled code"]
        E --> I["4. Test Reachability: Every slice fully tested"]
    end
    
    Subagent Verification Rubric --> J{"Can Diff Be Split Vertically?"}
    J -->|Yes| K["Propose gh-stack Plan & Present to User"]
    J -->|No / Valid Exception| L["Generate Exception Justification & Ask User Approval"]
    
    K --> M["Execute gh-stack Workflow"]
    L -->|Approved| D
```

---

## 1. Diff Sizing & Threshold Evaluation

Before requesting user confirmation to open a PR (`gh pr create`), the agent MUST evaluate the proposed diff size using `tools/analyze_diff_size.py`:

```bash
./tools/analyze_diff_size.py --base-ref origin/main
```

### Sizing Criteria & Thresholds

| Category | Threshold | Measurement Rule |
| :--- | :--- | :--- |
| **Production Code** | **$\ge 400$ lines** | Added + modified lines across production files (`.go`, `.py`, `.ts`, `.js`, `.yaml`, `.tf`, `.proto`). |
| **Total Code (Prod + Tests)** | **$\ge 1,000$ lines** | Combined production and test code (`*_test.go`, `*_test.py`, `*.spec.ts`, `test/**`). |
| **Documentation / Markdown** | **$\ge 1,000$ lines** | Markdown files (`.md`, `.markdown`, `.rst`, `docs/**`). Excluded from code calculations. |
| **Generated Code & Lockfiles** | *Ignored* | Automatically excluded (`*.pb.go`, `package-lock.json`, `MODULE.bazel.lock`, `go.sum`). |

If `should_trigger_split_verifier` is `true`, the agent **must not** open a monolithic PR directly. Instead, proceed to Section 2.

---

## 2. Launching the PR Split Verification Subagent

Spawn a dedicated `self` subagent to audit the change set and design a vertical splitting strategy:

```markdown
Role: PR Split & Architecture Verifier
Prompt:
You are an expert Google Staff Software Engineer and Architect reviewing a proposed diff exceeding the repository sizing threshold.

Current Diff Summary:
- Production Lines: <prod_lines>
- Test Lines: <test_lines>
- Documentation Lines: <doc_lines>
- Exceeded Reasons: <exceeded_reasons>

Touchpoint Files:
<list_of_files>

Your Task:
1. Conduct a strict Single Responsibility Principle (SRP) audit of the changes.
2. Evaluate whether this change can be vertically split into 2 or more smaller, cohesive layers.
3. Prioritize VERTICAL SPLITS (slicing by end-to-end user flow / feature slice) over horizontal architectural layers (slicing DB vs API vs UI).
4. Verify the Dead Code & Reachability Invariant: Intermediate PRs MUST NOT introduce dead, uncalled, or unreachable code. Every slice must be independently tested via unit/integration/E2E tests.
5. If splittable, produce a concrete `gh-stack` execution plan with branch names, commit messages, and file allocations.
6. If the change is an indivisible exception (e.g. monolithic deletion/migration), provide a rigorous technical justification.
```

---

## 3. Vertical Splitting Rubric & Invariants

The verification subagent must evaluate the diff against the following four core criteria:

### Pillar 1: Vertical Slices over Horizontal Layers
* **Vertical Slicing (Mandated Priority)**: Deconstruct work by end-to-end user capabilities. Each layer in the stack contains schema/model changes, backend logic, API endpoints, and tests required for that specific capability.
* **Horizontal Slicing (Forbidden Anti-Pattern)**: Splitting by architectural tiers (e.g., PR 1 = All database tables, PR 2 = All business logic, PR 3 = All HTTP handlers). This violates reachability and leaves uncalled code in trunk.

### Pillar 2: Dead Code & Test Reachability Invariant
* **No Orphaned Logic**: Every newly introduced function, struct, or endpoint must be directly referenced and reachable in the slice where it lands.
* **End-to-End Testability**: Each slice must pass local test matrices (`bazel test //...`) and include dedicated unit/integration tests exercising the new functionality.
* **Foundational Contracts**: If a foundational interface or base type must land first without full operational consumption, it must be validated by complete unit test fixtures and test doubles in that same layer.

### Pillar 3: Documentation & Rule Modularity (for Markdown diffs)
* When documentation or agent rules exceed 1,000 lines, decompose them across separate PRs by **Domain / Topic / Lifecycle Phase** (e.g. Core Architecture Rules in PR 1, PR Lifecycle in PR 2, Language Guidelines in PR 3).
* Ensure relative markdown links and cross-references remain valid in each intermediate layer.

### Pillar 4: Exceptions & Justification Criteria
A diff exceeding thresholds is acceptable only under strict architectural exceptions:
1. **Mechanical Automated Migrations**: Bulk automated renames, import path adjustments, or API deprecation removals.
2. **Monolithic Code Deletions**: Removing obsolete packages, unused services, or deprecated features.
3. **Atomic Single-Document Specs**: A single unified design doc or RFC where splitting would destroy narrative flow.

---

## 4. Constructing the `gh-stack` Plan

When the subagent determines that a diff can be split, format the plan using the [`gh-stack`](conversation://skills/gh-stack) conventions:

```markdown
### Proposed Vertical PR Stack (via `gh-stack`)

```
(main) <- feat/layer-1-core-slice <- feat/layer-2-extended-flow
```

#### Layer 1: `<prefix>/<layer-1-name>` (Base Slice)
- **Scope**: <Concise summary of minimal vertical flow>
- **Files**:
  - `pkg/domain/model.go`
  - `pkg/domain/handler.go`
  - `pkg/domain/handler_test.go`
- **Reachability & Verification**: <How this layer is tested/reachable without dead code>

#### Layer 2: `<prefix>/<layer-2-name>` (Upstack Slice)
- **Scope**: <Concise summary of dependent capability>
- **Files**:
  - `pkg/controller/reconciler.go`
  - `pkg/controller/reconciler_test.go`
  - `test/e2e/workflow_test.go`
- **Reachability & Verification**: <How this layer builds upon Layer 1>
```

---

## 5. User Confirmation & Decision Gate

Present the subagent's findings to the user. Do NOT split branches or open PRs without user confirmation:

### Scenario A: Split Recommended
> *"The proposed changes total **<total_code_lines> lines** (Production: <prod_lines>, Tests: <test_lines>), exceeding the PR sizing threshold.*
> 
> *The PR Split Verification Subagent recommends splitting this change into **<N> vertical slices** using `gh-stack`:*
> 
> *[Embed gh-stack Plan from Section 4]*
> 
> *Shall I proceed with creating this stacked PR structure using `gh-stack`? (Y/n)"*

### Scenario B: Exception Justification
> *"The proposed changes total **<total_code_lines> lines**, exceeding the PR sizing threshold.*
> 
> *The PR Split Verification Subagent evaluated the diff and determined it is an **atomic change exception**:*
> - **Reason**: <Technical justification, e.g., mechanical refactor / atomic schema migration>
> - **Risk Assessment**: <Why splitting would introduce intermediate broken states or dead code>
> 
> *Shall I proceed with opening this as a single Pull Request? (Y/n)"*
