---
name: XMC Draft Release Notes
description: Gathers incremental merged PRs across a specified timeframe (default 7 days / 1 week), semantically clusters PRs into proposed active project initiatives, aligns interactively with the user on project groupings, and drafts customer-centric release notes focusing strictly on delivered user value.
---

# Customer-Centric Release Notes Drafting Playbook

This skill automates gathering merged pull requests within a target timeframe (default: past 7 days / 1 week), semantically clustering PRs into active project domains and initiatives, aligning interactively with the user on the project categorization, and translating technical diffs into customer-centric release notes.

> [!IMPORTANT]
> **Dynamic Semantic Grouping & Interactive Alignment**:
> Do NOT use static or hardcoded project lists. Read the actual PR titles, scopes, changed files, and descriptions to understand the engineering initiatives, present a dynamic project breakdown proposal to the user for alignment, and only draft the final release notes once the project groupings are confirmed.
> All release notes MUST emphasize **delivered customer value** ("What can users or operators do now?", "How does this improve performance or reliability?") rather than internal code mechanics.
> Place all features directly under their corresponding project domain (do not separate features into an artificial "feature flags" section).
> Strictly follow the [Skills & Automation Guidelines](../../rules/skills_and_automation.md) and [GitHub Authoring & PR Creation Standards](../../rules/github_authoring_and_prs.md).

---

## Guidelines for Using CLI Tools

When invoking helper tools under `tools/` (e.g., `extract_release_prs.py`):
1. **Do not read tool source code**: Do not open or read the Python source code of helper tools. Refer to [Skills & Automation Guidelines](../../rules/skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints).
2. **Use `--help`**: Run tools with `--help` to inspect parameters and usage.
3. **Parse Structured Output & Handle Errors**: All tools output structured JSON (exit code 0 for success, 1 for handled error, 2 for unhandled error).
4. **Shell & Payload Safety**: Execute standalone `.py` script files or pass arguments via script parameters. Avoid double-quoted inline commands (`python3 -c "..."`).

---

## Interactive 4-Phase Workflow

### Phase 1: PR Discovery & Extraction

Execute `tools/extract_release_prs.py` to extract merged PRs and their metadata for the target timeframe:

- **Default (Last 7 Days / 1 Week)**:
  ```bash
  python3 tools/extract_release_prs.py --days 7
  ```
- **Custom Timeframe / Sprint Range**:
  ```bash
  python3 tools/extract_release_prs.py --since 2026-08-19 --until 2026-08-26
  ```

Parse the resulting JSON output to obtain the list of PRs with their metadata:
- `number`, `title`, `scope`, `author`, `merged_at`, `url`, `labels`, `files`
- Extracted markdown sections (`sections`: `summary`, `key changes`, `motivation & context`, `verification & testing`)
- `raw_body`

---

### Phase 2: Semantic Project Clustering & Domain Proposal

Read the extracted PR metadata and semantically analyze the active initiatives represented across the batch:

1. **Understand Active Themes**:
   - Inspect PR scopes (e.g., `dashboard`, `ui`, `rod`, `cms`, `actuator`, `ess`, `ci`, `ctk_v2`, `helm`).
   - Read PR titles, modified file paths, summaries, and motivation sections to understand the real user capabilities or infrastructural workstreams underway.
2. **Formulate Tailored Project Categories**:
   - Dynamically create descriptive, meaningful project groupings matching the work done in this period (e.g. `📊 Custom Dashboards & Visualization (xdash / XMetrics)`, `🔭 Research Observability & Queue Telemetry (ROD)`, `🧩 Component Management System (CMS)`, `⚡ Workload Orchestration & JobSet Execution`, `🛠️ CI Performance & Infrastructure Automation`).
   - Group related PRs under their primary initiative.
   - Flag any breaking changes or deprecations identified across the PRs.
3. **Draft the Proposal Breakdown**:
   - Prepare a structured summary containing each proposed project group, a 1-2 sentence description of the initiative, and the list of mapped PRs (`#<number>: <title> (<author>)`).

---

### Phase 3: Interactive User Alignment Gate

> [!IMPORTANT]
> **Mandatory User Confirmation Gate**:
> Present the proposed project breakdown to the user in the conversation before drafting the final release notes. Do NOT proceed directly to Phase 4 without user confirmation or feedback.

Present the proposal in a clear format:
```markdown
### 📋 Proposed Project Domains & PR Groupings

I have analyzed the **<Total PRs>** merged PRs from `<Start Date>` to `<End Date>` and clustered them into the following proposed project domains:

1. **<Project / Initiative Name & Emoji>**
   * *Theme*: <1-2 sentence overview of the initiative>
   * *Mapped PRs*:
     - #<PR> - <PR Title> (@<author>)
     - #<PR> - <PR Title> (@<author>)

2. **<Project / Initiative Name & Emoji>**
   * *Theme*: <1-2 sentence overview of the initiative>
   * *Mapped PRs*:
     - #<PR> - <PR Title> (@<author>)

...

---
**Next Step**: Please confirm if this project categorization looks good, or let me know if you would like to rename, merge, split, or reassign any groupings before I draft the customer-facing release notes.
```

**Alignment Handling**:
- If the user **confirms** (e.g. "looks good", "proceed", "yes"): Proceed to Phase 4 using the proposed groupings.
- If the user **requests adjustments** (e.g. "rename X to Y", "merge group 1 and 2", "move PR #123 into group 3"): Update the project groupings according to the user's feedback and proceed to Phase 4.

---

### Phase 4: Customer-Centric Release Notes Drafting

Using the user-confirmed project groupings, draft the release notes following customer-centric value transformation principles:

1. **Transform Technical Diffs into Customer Value**:
   - Ask: *"What capability or improvement does the researcher, developer, or cluster operator experience?"*
   - Avoid internal mechanics (e.g., replace *"Added DeleteChart gRPC handler and Spanner mutation"* with *"Dashboard authors can now permanently delete obsolete charts with fine-grained access control"*).
2. **Combine Related PRs into Cohesive Deliverables**:
   - If multiple PRs contribute to the same initiative (e.g., 3 PRs building dashboard charts), synthesize them into a single bullet point referencing all PR numbers (`#531, #532, #533`).
3. **Sub-Categorize within Projects** (when helpful):
   - **✨ Capabilities & Usability**: New features, controls, and workflows.
   - **🐛 Stability & Fixes**: Resolved defects and reliability improvements with user impact context.
4. **Structure & Template**:
   Format the release notes strictly following this markdown template:

```markdown
# 🚀 XMC Release Notes (<Start Date> – <End Date>)

> **Period Evaluated**: `<Start Date>` to `<End Date>` | **Total PRs Delivered**: `<Count>`

---

## <Emoji> 1. <Confirmed Project 1 Name>
* **<Feature Name>**: <Customer-facing value statement explaining what users can now do> ([#<PR>](https://github.com/google/xmc/pull/<PR>)).
* **<Feature Name>**: <Customer-facing value statement> ([#<PR1>](https://github.com/google/xmc/pull/<PR1>), [#<PR2>](https://github.com/google/xmc/pull/<PR2>)).
* **<Bug Fix Name>**: Fixed <issue description> so that <expected user outcome> ([#<PR>](https://github.com/google/xmc/pull/<PR>)).

---

## <Emoji> 2. <Confirmed Project 2 Name>
* **<Feature Name>**: <Customer-facing value statement explaining what researchers/operators can view or monitor> ([#<PR>](https://github.com/google/xmc/pull/<PR>)).
* **<Bug Fix Name>**: Fixed <issue description> ensuring <outcome> ([#<PR>](https://github.com/google/xmc/pull/<PR>)).

---

## ⚠️ Breaking Changes & Deprecations *(Include only if present)*
* **<Change Description>**: <What changed and required migration action> ([#<PR>](https://github.com/google/xmc/pull/<PR>)).
```

5. **Save & Present**:
   - Save the finalized release notes to an artifact (e.g. `release_notes_<date>.md`).
   - Present the draft release notes clearly in the conversation for final review.
