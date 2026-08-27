# Developer & Automation Tools (`tools/`)

This directory contains modular Python automation scripts, developer environment tooling, GitHub PR review utilities, benchmark comparison harnesses, and diagnostic tools for the XManager on Cloud (XMC) platform.

---

## Directory Organization

The tools suite is organized into domain-focused subpackages:

```
tools/
├── common/             # Shared utilities, virtualenv management, and container builders
├── dev_env/            # Developer environment deployment and live upgrade tools
├── pr_review/          # GitHub PR review automation, audit generation, and comment verification
├── benchmark/          # Subagent trajectory benchmarks, telemetry extraction, and diff analysis
├── xmc-diag/           # Cluster diagnostics and health inspection utilities
└── *.py                # Backward-compatible forwarding wrappers
```

---

## Subpackages Overview

### 1. `tools/common/` (Shared Infrastructure)
Centralized utilities and build helpers used across tools:
- [`tool_utils.py`](common/tool_utils.py): Shared virtual environment isolation, handled exception formatting, and unified CLI execution runners.
- [`build_oci.py`](common/build_oci.py): Docker/OCI container image building and registry push automation.
- [`setup_venv.py`](common/setup_venv.py): Automated local virtual environment initialization from `requirements.txt`.
- [`workspace_status.py`](common/workspace_status.py): Git workspace and branch status inspection.
- [`requirements.txt`](common/requirements.txt): Pinned third-party Python dependencies for developer tools.

### 2. `tools/dev_env/` (Developer Environment Automation)
Playbooks for deploying and updating local/GKE development clusters:
- [`deploy_dev_env.py`](dev_env/deploy_dev_env.py): Orchestrates Cluster Toolkit (`gcluster`) compilation and step-by-step deployment.
- [`upgrade_dev_env.py`](dev_env/upgrade_dev_env.py): Rebuilds changed services, pushes OCI images, and rolls out Helm charts or deployments without recreating infrastructure.

### 3. `tools/pr_review/` (PR Review & GitHub Automation)
Utilities supporting multi-agent PR review and authoring workflows:
- [`github_utils.py`](pr_review/github_utils.py): Robust GitHub REST and GraphQL API client with automatic token resolution (`gh auth` / `GITHUB_TOKEN`).
- [`review_pr_generate_audit.py`](pr_review/review_pr_generate_audit.py): Generates interactive Markdown review audit checklists and existing comment deduplication indexes.
- [`review_pr_post_inline_comment.py`](pr_review/review_pr_post_inline_comment.py): Posts confirmed inline code review comments or replies to PR diffs.
- [`review_pr_verify_posted.py`](pr_review/review_pr_verify_posted.py): Verifies programmatically that 100% of approved review checklist comments are present on GitHub.
- [`fetch_pr_comment.py`](pr_review/fetch_pr_comment.py): Fetches review comments and discussion threads by URL or PR number.
- [`reply_pr_comment.py`](pr_review/reply_pr_comment.py): Posts inline replies to specific PR review comments with telemetry tags.
- [`generate_review_checklist.py`](pr_review/generate_review_checklist.py): Extracts unresolved review comments into a Markdown action checklist.
- [`verify_pr_comment_resolution.py`](pr_review/verify_pr_comment_resolution.py): Verifies 1-to-1 thread resolution for all comments in a review checklist.
- [`update_pr_description.py`](pr_review/update_pr_description.py): Updates GitHub PR descriptions from text or Markdown files.

### 4. `tools/benchmark/` (Skills & Rules Benchmark Suite)
Harness for testing and evaluating skill/rule modifications:
- [`compare_trajectories.py`](benchmark/compare_trajectories.py): Parses and compares subagent `transcript.jsonl` trajectories (turns, tokens, tool calls, outcomes).
- [`analyze_diff_and_synthesize.py`](benchmark/analyze_diff_and_synthesize.py): Extracts structured git diffs and categorized changes across `.agents/`.
- [`extract_telemetry.py`](benchmark/extract_telemetry.py): Extracts usage telemetry embedded in GitHub PR comments.
- [`resolve_test_scope.py`](benchmark/resolve_test_scope.py): Resolves affected Bazel test scopes from git diffs for CI optimization.
- [`teardown_benchmark.py`](benchmark/teardown_benchmark.py): Cleans up test PRs, branches, and scratch artifacts generated during benchmark runs.

---

## Backward Compatibility & Forwarding Wrappers

For backward compatibility with existing developer scripts and agent skill definitions, top-level forwarding wrappers and repository-level utilities are maintained directly in `tools/`:
- **Repository Entrypoints & Hooks**: Tools invoked directly by git hooks or top-level entrypoints (`analyze_diff_size.py`, `dev_env.py`) remain at the `tools/` root for immediate access.
- **Forwarding Wrappers**: Executing `./tools/<script>.py` or `python tools/<script>.py` transparently delegates to the corresponding modular script in `tools/<subpackage>/<script>.py`.
- **Bazel Aliases**: Bazel targets under `//tools:...` alias to their respective `//tools/<subpackage>:...` targets.

---

## Testing

Run all unit tests across all tools subpackages:

```bash
bazel test //tools/...
```
