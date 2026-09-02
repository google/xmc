---
name: XMC Address PR Comment
description: Interactive PR comment resolution workflow enforcing comment integrity, conversation artifact hygiene, mandatory human confirmation gates, zero unapproved posting, and 1-to-1 thread resolution verification.
---

# PR Comment-Driven Development Workflow

This document defines a structured agent workflow that automates the process of addressing GitHub Pull Request comments while enforcing comment integrity, conversation artifact hygiene, mandatory human confirmation gates, and 1-to-1 thread resolution verification.

```mermaid
graph TD
    A["User provides PR / comment link"] --> B["Phase 1: Run generate_review_checklist.py (-o <appDataDir>/brain/<conv_id>/pr_review_checklist.md)"]
    B --> C["Phase 2: Fetch comment context (fetch_pr_comment.py) & Impact Mapping"]
    C --> D["Phase 2: Prepare pr_comment_resolution_plan.md artifact"]
    D --> E{"Phase 3: Mandatory Human Confirmation Gate (Stop Execution)"}
    E -->|User confirms / approves plan (Y)| F["Phase 4: Implement Code Changes & Unit Tests"]
    E -->|User requests adjustments| D
    E -->|User cancels / rejects| Z["End session without code changes or GitHub posts"]
    F --> G["Phase 5: Local Commit (Conventional Commit + TAG=agy)"]
    G --> H["Phase 6: Post Approved Thread Replies (reply_pr_comment.py)"]
    H --> I["Phase 7: Build & Push OCI Images / GKE Rollout (if applicable)"]
    I --> J["Phase 8: 1-to-1 Thread Resolution Verification (verify_pr_comment_resolution.py)"]
    J --> K["Phase 9: Root-Cause Retrospective & Iterative Reflection Proposal"]
    K --> L{"User approves reflection?"}
    L -->|Yes| M["Invoke /XMC Iterative Reflection"]
    M --> N["Complete"]
    L -->|No / Done| N
```

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the mandatory [PR Review & Inspection Guidelines](../../rules/pr_review_guidelines.md), [Skills & Automation Guidelines](../../rules/skills_and_automation.md), and [Core Engineering Standards](../../rules/core_engineering.md).

---

## Guidelines for Using CLI Tools

When invoking helper tools under `tools/` (e.g. `tools/pr_review/generate_review_checklist.py`, `tools/pr_review/fetch_pr_comment.py`, `tools/pr_review/reply_pr_comment.py`, `tools/common/build_oci.py`):
1. **Do not read tool source code**: Do not open or read the Python source code of helper tools. Refer to [Skills & Automation Guidelines](../../rules/skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints).
2. **Use `--help`**: To learn about the tool's usage, options, and parameters, run it with the `--help` flag (e.g. `./tools/pr_review/fetch_pr_comment.py --help`).
3. **Parse Structured Output & Handle Errors**: All tools output structured JSON (exit code 0 for success, 1 for handled error, 2 for unhandled error).
4. **Store Artifacts strictly inside Conversation Directory (`<appDataDir>/brain/<conv_id>/`)**: All generated markdown review checklists (`pr_review_checklist.md`) and resolution plans (`pr_comment_resolution_plan.md`) must be saved in the conversation artifact directory (`<appDataDir>/brain/<conv_id>/`) via `-o` rather than the workspace root to prevent accidental git repository commits or worktree pollution.
5. **Shell & Payload Safety**: Execute standalone `.py` script files or pass arguments via script parameters. Avoid double-quoted inline commands (`python3 -c "..."`).
   - **Backtick Safety**: When passing reply messages containing backticks (e.g. markdown code spans), avoid double-quoted string arguments in bash. Use `--message-file` (or `-f`) to safely read the reply message from a file instead.



---

## Phase 1: Automated Review Checklist Generation

When addressing PR review feedback or starting a review pass on a Pull Request, the agent first generates a checklist of all unresolved comments directly from the GitHub API, saving the artifact directly inside the conversation directory to avoid workspace repository pollution.

### When to Call
- **PR Review Start**: Call immediately when beginning to process review comments on a PR.
- **Review Pass Check**: Call during post-PR creation checks or whenever an updated checklist of unresolved comments is needed.

### Execution Command
```bash
./tools/pr_review/generate_review_checklist.py <owner> <repo> <pr_number> -o <appDataDir>/brain/<conversation_id>/pr_review_checklist.md
```

### Result Output Schema
```json
{
  "status": "success",
  "result": {
    "owner": "google",
    "repo": "xmc",
    "pr_number": 145,
    "output_file": "<appDataDir>/brain/<conv_id>/pr_review_checklist.md",
    "unresolved_comment_count": 3
  }
}
```
This generates `pr_review_checklist.md` inside `<appDataDir>/brain/<conversation_id>/` populated with interactive `- [ ]` checkboxes for each unresolved comment.

---

## Phase 2: Comment Extraction, Impact Mapping & Resolution Strategy Plan

The agent accepts a comment link or PR number. The helper script `tools/pr_review/fetch_pr_comment.py` or GitHub API queries retrieve the raw comment context for all unresolved items.

### Fetch Comment Context
```bash
./tools/pr_review/fetch_pr_comment.py "<github_comment_url>"
```

### Result Schema
```json
{
  "type": "review_comment",
  "comment_id": 12345678,
  "author": "reviewer_username",
  "body": "Comment text describing the request (e.g., 'Refactor this to avoid nested if-else checks')",
  "file_path": "pkg/auth/oauth2/handlers.go",
  "line": 150,
  "diff_hunk": "@@ -148,5 +148,8 @@...",
  "commit_id": "a1b2c3d4e5f6..."
}
```

### Dependency Mapping & Impact Analysis
To make the workflow precise, the agent figures out which Bazel binaries and Kubernetes deployments are impacted by the changed file.

#### Bazel Package Dependency Mapping
The agent runs `bazel query` to discover all `go_binary` targets that transitively depend on the modified package:

```bash
# 1. Identify the package of the modified file
# E.g. file_path = "pkg/auth/oauth2/handlers.go" -> package //pkg/auth/oauth2

# 2. Query all binaries depending on it
bazel query "kind(go_binary, rdeps(//..., //pkg/auth/oauth2))"
```

*Example Output:*
- `//cmd/xid_service:xid_service`
- `//cmd/experiment_state_server:experiment_state_server`

#### Mapping Binaries to GKE Deployments
Each `go_binary` mapped above is associated with an OCI image builder and GKE deployment:

| Bazel Target | Push Command Target | Helm Template File | GKE Deployment Name |
| :--- | :--- | :--- | :--- |
| `//cmd/xid_service:xid_service` | `//cmd/xid_service:xid_service_image_push` | `deploy/helm/templates/xid/deployment.yaml` | `xid-service-deployment` |
| `//cmd/experiment_state_server` | `//cmd/experiment_state_server:ess_image_push` | `deploy/helm/templates/ess/deployment.yaml` | `experiment-state-server-deployment` |
| `//cmd/actuator:actuator` | `//cmd/actuator:actuator_image_push` | `deploy/helm/templates/actuator/deployment.yaml` | `actuator-deployment` |
| `//cmd/reconciler:reconciler` | `//cmd/reconciler:reconciler_image_push` | `deploy/helm/templates/reconciler/deployment.yaml` | `reconciler-deployment` |
| `//cmd/log_monitor:log_monitor` | `//cmd/log_monitor:log_monitor_image_push` | N/A | N/A |
| `//cmd/ui` (Frontend TS/HTML) | `//cmd/ui:ui_image_push` | `deploy/helm/templates/ess/ui-deployment.yaml` | `ui-deployment` |
| `//cmd/health_service:health_service` | `//cmd/health_service:health_service_image_push` | `deploy/helm/templates/health-service/deployment.yaml` | `health-service-deployment` |
| `//cmd/rod_data_provider:rod_data_provider` | `//cmd/rod_data_provider:rod_data_provider_image_push` | `deploy/helm/templates/rod/deployment.yaml` | `rod-data-provider-deployment` |

### Prepare Resolution Strategy Plan Artifact (`pr_comment_resolution_plan.md`)
The agent prepares a comprehensive implementation plan formatted as a Markdown artifact inside `<appDataDir>/brain/<conv_id>/pr_comment_resolution_plan.md`.

For each unresolved review comment, classify the proposed resolution strategy into one of three distinct categories:
1. **`[APPLY FIX]`**: The comment highlights a valid defect or improvement. Specify:
   - Target file path and exact lines.
   - Analysis of reviewer concern.
   - Proposed code changes (git diff snippet).
   - Affected test packages and deployment targets.
2. **`[PUSH BACK / DISAGREE]`**: The comment suggests a change that violates codebase contracts, architectural invariants (e.g. Non-Restarting JobSets Contract `MaxRestarts: 0`), or design specifications. Specify:
   - Specific rule or contract violated.
   - Proposed technical justification to post as a reply.
3. **`[CLARIFY / QUESTION]`**: The reviewer request is ambiguous or lacks necessary context. Specify:
   - Clarifying question to post in reply to the reviewer.

---

## Phase 3: Mandatory Explicit Confirmation Gate (Zero Unapproved Actions)

> [!WARNING]
> **Strict Anti-Jumping & No-Unapproved-Action Guardrail**: The agent MUST NEVER edit code, execute tests, commit changes, push commits, build OCI images, rollout restart GKE deployments, or post replies on GitHub (`tools/reply_pr_comment.py`) without explicit human approval.
>
> The agent MUST halt execution after preparing the plan and present the comments summary and proposed actions directly in chat, waiting for user confirmation before taking ANY mutating action.

### Present Comments Summary & Await Confirmation
When Phase 2 plan preparation is complete, the agent presents a structured summary table in chat:

```markdown
### 📋 PR Review Comments Resolution Plan (PR #<pr_number>)

| Item | Comment ID | Author | Location | Reviewer Feedback Summary | Proposed Action | Proposed Resolution / Reply Preview |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `#1` | `12345678` | `@reviewer` | `pkg/auth/oauth2/handlers.go:150` | Avoid nested if-else checks | `[APPLY FIX]` | Refactor with early return guard clauses |
| `#2` | `12345679` | `@reviewer` | `deploy/helm/values.yaml:45` | Increase restart count to 3 | `[PUSH BACK]` | Maintain `MaxRestarts: 0` per Non-Restarting JobSets contract |
| `#3` | `12345680` | `@reviewer` | `pkg/client/client.go:88` | Unclear timeout semantics | `[CLARIFY]` | Ask if 30s context timeout is preferred over retry backoff |

*Detailed implementation plan and proposed diffs saved to `pr_comment_resolution_plan.md` in session artifacts.*
```

### Explicit Confirmation Prompt
The agent asks the user for explicit confirmation and **halts execution to await input**:

> *"Please inspect the proposed resolution plan above (and `pr_comment_resolution_plan.md`).*
> *1. **Shall I proceed with addressing the approved comments (#1 through #N) and implementing the fixes?** (`Y`/`n` / modify `#X`)*
> *2. **Are there any comments you would like to push back on, modify, or handle differently?**"*

> [!IMPORTANT]
> The agent MUST STOP here and wait for the human reviewer's response. Do NOT proceed to Phase 4, edit files, or post any replies until the user explicitly responds with approval.

---

## Phase 4: Code Implementation & Test Verification (Post-Approval Only)

Only after the user approves the resolution plan in Phase 3, the agent implements the agreed code changes and runs tests.

### Run Unit Tests
```bash
# Run unit tests for the affected package
bazel test //pkg/auth/oauth2/...
```

### Check Coverage
To verify test coverage, the agent runs:
```bash
bazel coverage --combined_report=lcov //pkg/auth/oauth2/...
```
The output coverage file is generated under `bazel-out/_coverage/_coverage_report.dat`.
The agent parses this file to report package test coverage percentage and ensure newly added code paths are tested.

---

## Phase 5: Local Commit & Build Verification

Once local unit tests pass and building the affected binary targets succeeds, the agent commits the code to the current branch.

### Build Targets Locally
Before committing or deploying, verify that the affected binaries build successfully:
```bash
bazel build //cmd/xid_service:xid_service
```

### Git Commit to Current Branch
The agent stages only modified source files (ensuring no brain artifacts or temporary scratch files are staged) and commits them as a new, distinct follow-up commit with a Conventional Commit message including `TAG=agy` and `CONV=<conversation_id>`:
```bash
git add pkg/auth/oauth2/handlers.go
git commit -m "fix(auth): address review comment on nested if-else checks

Comment link: <github_comment_url>

TAG=agy
CONV=<conversation_id>"
git push origin HEAD
```

> [!CAUTION]
> **Strict Prohibition of Force-Pushing on Active PRs**:
> NEVER use `git commit --amend` or `git push --force` (`-f`) when addressing review comments on an active PR branch.
> Always append distinct follow-up commits so reviewers can inspect incremental diffs via GitHub's **"Changes since last review"** view. Squashing happens automatically at merge time via GitHub's "Squash and merge" feature, NEVER during active review rounds.

---

## Phase 6: Controlled GitHub Notification & Reply Posting

> [!CAUTION]
> **Zero Unapproved Posting**: The agent must ONLY reply to comment threads that were explicitly approved in Phase 3. Never post unapproved replies or replies to comments the user asked to skip.

The agent runs `tools/pr_review/reply_pr_comment.py` to notify the reviewer that the changes have been made or post the approved pushback/clarification:

```bash
cat << 'EOF' > reply_message.txt
Addressed review comment for `handlers.go`: refactored with linear early guard returns per `core_engineering.md`.
EOF
./tools/pr_review/reply_pr_comment.py "<github_comment_url>" --message-file reply_message.txt
```

> [!WARNING]
> **Backtick Subshell Command Safety**: Avoid passing double-quoted strings containing backticks (`` ` ``) directly on the command line (e.g. `./tools/pr_review/reply_pr_comment.py "<url>" "Fixed `func_name()`"`). Bash evaluates backticks in double-quoted strings as subshell commands prior to executing python, causing unwanted command execution or syntax errors. Always use `--message-file` (or `-f`) when the reply message contains backticks or markdown code formatting.

### Usage Telemetry Tracking
When `tools/pr_review/reply_pr_comment.py` successfully posts a reply, it appends a hidden HTML comment block containing public usage metadata (authenticated GitHub user, comment ID, PR number, and UTC timestamp) to the comment body:
```html
<!-- xmc-telemetry: {"git_username": "Chad", "date": "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29", "comment_id": 1234} -->
```

To extract this telemetry and output it as a raw JSON dataset, run:
```bash
./tools/benchmark/extract_telemetry.py <owner> <repo> [--since <ISO_DATE>]
```

---

## Phase 7: Build, Push, and GKE Redeployment (When Applicable)

Using the `.env` configuration file, the agent updates the deployment environment if the modified components require cluster rollout.

### Target Environment (Extracted from `.env`)
- **GCP Project ID**: `xmc-test-project-6`
- **GKE Cluster**: `xmc-cttest8d-cttest8e`
- **GCP Region**: `europe-central2`
- **K8S Namespace**: `xmc`
- **Artifact Registry Target**: `us-docker.pkg.dev/xmc-test-project-6/xmc`

### Rebuild and Push Containers
For each affected binary (e.g. `xid_service`), the agent sets up the environment variables and runs the push script:
```bash
# Set build environment variables
export XMC_ACR_TARGET="us-docker.pkg.dev/xmc-test-project-6/xmc"
export XMC_ENV="dev"

# Build and push the container image to GCP Artifact Registry
bazel run //cmd/xid_service:xid_service_image_push
```

### Rollout Restart in GKE Cluster
The agent initiates a rolling update to download the newly pushed image (tagged `:latest`):

```bash
# 1. Authenticate to the GKE Cluster
gcloud container clusters get-credentials xmc-cttest8d-cttest8e \
    --region europe-central2 \
    --project xmc-test-project-6

# 2. Restart the deployment to pick up the updated container image
kubectl rollout restart deployment/xid-service-deployment -n xmc

# 3. Monitor rollout status
kubectl rollout status deployment/xid-service-deployment -n xmc
```

---

## Phase 8: 1-to-1 Thread Resolution Verification

Before marking the PR review task as complete:
1. Run `./tools/pr_review/verify_pr_comment_resolution.py <owner> <repo> <pr_number> <appDataDir>/brain/<conversation_id>/pr_review_checklist.md` to programmatically verify that 100% of approved comment IDs from the checklist have received dedicated inline replies.
2. Confirm that every single approved comment ID from the consolidated checklist artifact has received a dedicated inline response or resolution.
3. Update the checklist artifact status for each item to `[x] Resolved`.

---

## Phase 9: Root-Cause Retrospective & Iterative Reflection Proposal

Whenever review comments (from human reviewers or automated review bots) are addressed and verified, the agent must not merely "fix and forget". The agent performs a fast root-cause classification on why the initial PR implementation missed the expected standard and proposes invoking the [Iterative Reflection Skill](../xmc_iterative_reflection/SKILL.md).

### Root-Cause Classification Framework
Classify each addressed review comment into one of three categories:
1. **Rule & Guideline Gap (`.agents/rules/`)**: The codebase guidelines lacked an explicit rule, style constraint, or invariant (e.g., missing error wrapping convention, naming convention, or mock contract).
2. **Tooling & Automation Gap (`tools/`)**: The feedback could have been caught mechanically before PR submission by a script, linter, or pre-commit hook (e.g., missing parameter validation, formatting check, or test coverage threshold).
3. **Skill & Workflow Gap (`.agents/skills/`)**: The development workflow playbook lacked a necessary pre-PR inspection step or verification phase.

### Presenting the Retrospective Proposal
At the end of the comment resolution report, present a structured proposal table and ask the user if they wish to trigger iterative reflection:

```markdown
### 🔄 PR Comment Retrospective & Meta-Learning Proposal

| Comment ID | Reviewer Feedback Summary | Root-Cause Category | Proposed Rule / Tool Enhancement |
| :--- | :--- | :--- | :--- |
| #1234 | "Wrap internal errors with `%w`" | Rule Gap (`.agents/rules/languages/golang.md`) | Add explicit Go error wrapping rule |
| #1235 | "Missing unit test for helper function" | Skill Gap (`.agents/skills/xmc_feature_development/SKILL.md`) | Enforce 1-to-1 helper unit test check |

> Would you like to invoke `/XMC Iterative Reflection` to analyze these root causes, benchmark candidate rule updates with `/XMC Trajectory Comparison`, and open a dedicated improvement PR?
```

If the user approves, proceed to invoke the [XMC Iterative Reflection Skill](../xmc_iterative_reflection/SKILL.md).

---

## Workflow Example

Here is a step-by-step example of how this skill executes:

1. **You ask**:
   > Address PR comments for: `https://github.com/google/xmc/pull/15`

2. **The agent executes (Phase 1 & Phase 2)**:
   - Runs `./tools/generate_review_checklist.py google xmc 15 -o <appDataDir>/brain/<conv_id>/pr_review_checklist.md`.
   - Extracts 100% of open threads (e.g., 3 comments total) via `tools/fetch_pr_comment.py`.
   - Identifies code locations and runs `bazel query` to map changes to affected binaries (`//cmd/xid_service` and `//cmd/experiment_state_server`).
   - Prepares `pr_comment_resolution_plan.md` inside `<appDataDir>/brain/<conv_id>/`.

3. **The agent PAUSES at Phase 3 (Mandatory Confirmation Gate)**:
   - Displays structured summary table of all 3 comments in chat with proposed actions (`[APPLY FIX]`, `[PUSH BACK]`, `[CLARIFY]`).
   - Asks: *"Shall I proceed with addressing the approved comments (#1 through #3)? (Y/n / modify #X)"*
   - **Stops calling tools and waits for your response.**

4. **You approve the plan** (e.g. `Y` or `Proceed, but push back on #2`).

5. **The agent executes (Phase 4, 5, 6, 7 & 8)**:
   - Edits the approved files.
   - Runs unit tests and coverage calculations.
   - Builds the affected targets locally.
   - Commits changes to the branch (`TAG=agy`, `CONV=<conversation_id>`).
   - Posts reply comments for approved comment IDs on GitHub using `reply_pr_comment.py --message-file`.
   - Pushes updated OCI containers via `bazel run //cmd/xid_service:xid_service_image_push`.
   - Restarts GKE pods: `kubectl rollout restart deployment/xid-service-deployment -n xmc`.
   - Programmatically verifies 1-to-1 thread resolution for all approved comment IDs:
     `./tools/verify_pr_comment_resolution.py google xmc 15 <appDataDir>/brain/<conv_id>/pr_review_checklist.md`.
   - Updates checklist artifact and replies with test outputs and rollout status confirmation.

6. **The agent concludes (Phase 9)**:
   - **Presents PR Comment Retrospective table** and proposes invoking `/XMC Iterative Reflection` to codify learnings into `.agents/rules/` and prevent future review feedback.
