# Developer & Automation Tools (`tools/`)

This directory contains modular Python automation scripts, developer environment tooling, GitHub PR review utilities, benchmark comparison harnesses, and diagnostic tools for the XManager on Cloud (XMC) platform.

---

## Directory Organization

The tools suite is organized into domain-focused subpackages:

```
tools/
├── common/             # Shared utilities, virtualenv management, and container builders
├── components/         # XMC component profiles discovery runner and linting harness
├── dev_env/            # Developer environment deployment and live upgrade tools
├── pr_review/          # GitHub PR review automation, audit generation, and comment verification
├── benchmark/          # Subagent trajectory benchmarks, telemetry extraction, and diff analysis
├── lint/               # Static analyzers, AST syntax walkers, and test determinism linters
├── migrations/         # One-time operator migration scripts (e.g. legacy metrics -> Components)
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
Playbooks and unified CLI for deploying, upgrading, and managing local/remote development clusters:
- [`dev_env.py`](dev_env/dev_env.py): Unified CLI entrypoint for all environment lifecycle operations (`up`, `down`, `reload`, `gke-deploy`, `gke-upgrade`, `deploy`, `upgrade`, `cluster`, `status`, `logs`, `spanner`, `build-images`).
- [`config.py`](dev_env/config.py): Configurable local development environment settings (`LocalDevConfig`), multi-source resolution precedence (CLI > Env > YAML > Defaults), and dynamic Spanner / database naming.
- [`change_detector.py`](dev_env/change_detector.py): Git diff inspection and smart mapping of file modifications to affected microservices and Helm charts.
- [`local_runner.py`](dev_env/local_runner.py): Local KinD orchestrator with single-shot and continuous watch mode (`-w`) fast reload, debouncing, and live Helm deployments.
- [`kind_manager.py`](dev_env/kind_manager.py): Pre-flight CLI tool verification, KinD cluster creation/deletion, credentials/secret provisioning (`gcp-sa-key`, `backend-oauth-secrets`, self-signed TLS certs), and local manifest application (`dummy_crds.yaml`, `redis.yaml`, `dex.yaml`, `gateway_proxy.yaml`).
- [`spanner_manager.py`](dev_env/spanner_manager.py): Cloud Spanner database existence verification, parallel creation with single-batch DDL & proto descriptors, schema migrations, and database dropping (`dev_env.py spanner`).
- [`image_builder.py`](dev_env/image_builder.py): Parallel Bazel OCI container image compilation, Docker archive loading, and loading images directly into KinD nodes (`dev_env.py build-images`).
- [`deploy_dev_env.py`](dev_env/deploy_dev_env.py): Orchestrates Cluster Toolkit (`gcluster`) compilation and step-by-step remote infrastructure rollout (invoked via `python3 tools/dev_env/dev_env.py gke-deploy`).
- [`upgrade_dev_env.py`](dev_env/upgrade_dev_env.py): Rebuilds changed services, pushes OCI images, and rolls out Helm charts or deployments without recreating infrastructure (invoked via `python3 tools/dev_env/dev_env.py gke-upgrade`).

### 3. `tools/pr_review/` (PR Review & GitHub Automation)
Utilities supporting multi-agent PR review and authoring workflows:
- [`github_utils.py`](pr_review/github_utils.py): Robust GitHub REST and GraphQL API client with automatic token resolution (`gh auth` / `GITHUB_TOKEN`).
- [`review_pr_generate_audit.py`](pr_review/review_pr_generate_audit.py): Generates interactive Markdown review audit checklists and existing comment deduplication indexes. With `--out-dir` it fetches the PR head (`--checkout` checks it out detached when tracked files are clean) and writes `pr.diff`, `pr_meta.json`, and `review_threads.json`; in re-review mode it reports lines changed since your last review and recommends a full review above `--full-review-threshold`.
- [`review_pr_workspace.py`](pr_review/review_pr_workspace.py): Library behind `--out-dir`/`--checkout`: fetches and optionally checks out `pull/<n>/head`, writes `pr.diff`, `pr_meta.json`, and `review_threads.json`, and measures the re-review delta since your last review.
- [`review_pr_post_inline_comment.py`](pr_review/review_pr_post_inline_comment.py): Posts confirmed inline code review comments or replies to PR diffs.
- [`review_pr_verify_posted.py`](pr_review/review_pr_verify_posted.py): Verifies programmatically that 100% of approved review checklist comments are present on GitHub. Reads the JSON sidecar written by `review_pr_generate_audit.py` (markdown fallback), fails when no approved items are found, and records items as posted via `--mark-posted`.
- [`fetch_pr_comment.py`](pr_review/fetch_pr_comment.py): Fetches review comments and discussion threads by URL or PR number.
- [`reply_pr_comment.py`](pr_review/reply_pr_comment.py): Posts inline replies to specific PR review comments with telemetry tags, optionally resolving the thread (`--resolve`). Outputs only the reply `id`, `html_url`, `in_reply_to`, and `resolved` status.
- [`review_threads.py`](pr_review/review_threads.py): Lists PR review threads with resolution and reply state, and resolves unresolved threads the authenticated user has replied to.
- [`generate_review_checklist.py`](pr_review/generate_review_checklist.py): Extracts unresolved review comments into a Markdown action checklist.
- [`verify_pr_comment_resolution.py`](pr_review/verify_pr_comment_resolution.py): Verifies 1-to-1 thread resolution for all comments in a review checklist.
- [`update_pr_description.py`](pr_review/update_pr_description.py): Updates GitHub PR title and/or description from text or Markdown files.
- [`wait_for_ci.py`](pr_review/wait_for_ci.py): Polls a PR's CI checks with backoff until they complete, judging only the newest run of each check; on failure, reports failed checks, workflow run/job IDs, and bounded, error-anchored job log tails.

### 4. [`tools/benchmark/`](benchmark/README.md) (Skills & Rules Benchmark Suite)
Harness for testing and evaluating skill/rule modifications (see [`tools/benchmark/README.md`](benchmark/README.md)):
- [`scenario_assertions.py`](benchmark/scenario_assertions.py): Evaluates pre-registered file and regex assertions (`file_exists`, `file_absent`, `file_contains`, `file_omits`) across `origin`, `transfer`, and `negative` scenarios and derives the deterministic 7-state benchmark verdict matrix.
- [`compare_trajectories.py`](benchmark/compare_trajectories.py): Parses and compares subagent `transcript.jsonl` trajectories (turns, tokens, tool calls, outcomes) and embeds the machine-derived [`scenario_assertions.py`](benchmark/scenario_assertions.py) verdict report via `--assertions`.
- [`teardown_benchmark.py`](benchmark/teardown_benchmark.py): Cleans up benchmark subagent processes and temporary Git worktrees.
- [`analyze_diff_and_synthesize.py`](benchmark/analyze_diff_and_synthesize.py): Extracts structured git diffs and categorized changes across `.agents/`.
- [`extract_telemetry.py`](benchmark/extract_telemetry.py): Extracts usage telemetry embedded in GitHub PR comments.
- [`resolve_test_scope.py`](benchmark/resolve_test_scope.py): Resolves affected Bazel test scopes from git diffs for CI optimization.

### 5. `tools/components/` (Component Manifest Linting & Profile Discovery)
Harness for automated validation and discovery of XMC component profiles:
- [`run_linter.py`](components/run_linter.py): Discovers component manifest directories across configured search roots (`deploy/profiles`, `deploy/components`, `examples/components/profiles`), resolves co-located `rules.yaml` files, and runs `component-linter` with consolidated text/JSON reporting.
- [`run_linter_test.py`](components/run_linter_test.py): Hermetic unit test suite for directory discovery, rules matching, exit codes, and JSON serialization.

### 6. `tools/release/` (Release Automation & Metadata Tooling)
Tooling and automation for release metadata extraction, SemVer calculations, and GitHub release publication:
- [`extract_release_metadata.py`](release/extract_release_metadata.py): Extracts git history, discovers dynamic feature flags, routes commits to audience tiers, and calculates SemVer recommendations.
- [`publish_release.py`](release/publish_release.py): Tags git commits, pushes remote tags, and publishes GitHub Releases via `gh` CLI.
- [`semver_engine.py`](release/semver_engine.py): Strict SemVer 2.0 recommendation engine and rationale generation.
- See [`tools/release/README.md`](release/README.md) for complete CLI documentation and schema references.

### 7. `tools/lint/` (Static Analyzers & Linters)
Offline syntax walkers and static validation tooling:
- [`check_go_determinism`](lint/check_go_determinism/): Ultra-fast (<50ms) Go AST analyzer checking Go test files for `os.Setenv` usage and arbitrary second-level `time.Sleep` calls.
- [`check_legacy_imports.py`](lint/check_legacy_imports.py): Ratchet lint preventing regression of google3 migration leftovers (legacy Go import roots, unresolved `xmanager_cloud/...` module paths, `compat_go_*` targets, `gazelle:resolve` overrides, `third_party/` proto paths, google3 Helm flags, legacy UI/Python paths) against the zeroed [`legacy_imports_baseline.json`](lint/legacy_imports_baseline.json). Fails when any count grows above zero. Runs in pre-commit and the lint CI job. Tracking: [#1066](https://github.com/google/xmc/issues/1066).
- [`check_agent_doc_paths.py`](lint/check_agent_doc_paths.py): Fails when agent docs (`.agents/**/*.md`, `AGENTS.md`) reference repo paths in code spans or fenced blocks that are not tracked files or directories. Placeholders (`<name>`, `{a,b}`, `$VAR`, `*`, `[`, `...`) only require their directory prefix to exist; runtime-created and illustrative paths live in its `ALLOWLIST` with reasons, and unreferenced entries fail. Runs in pre-commit (when agent docs are staged or files are deleted/renamed; always in `--check` mode) and the lint CI job.
- [`check_file_naming.py`](lint/check_file_naming.py): Enforces UI and workflow file-naming conventions across tracked repository files (`*.ng.html` for HTML files under `cmd/ui/`—templates under `cmd/ui/app/` with `cmd/ui/index.html` allowlisted—`*_test.ts` for Karma unit tests under `cmd/ui/app/` while rejecting `*.spec.ts`/`*_spec.ts`/`*.test.ts` repo-wide outside `cmd/ui/screenshot_tests/`, `*.screenshot.spec.ts` for Playwright visual specs under `cmd/ui/screenshot_tests/`, and top-level `*.test.js` for Node workflow tests under `.github/scripts/`). Runs in pre-commit and the lint CI job.

### 8. [`tools/migrations/components/`](migrations/components/README.md) (One-Time Component Cutover)
Temporary migration side script (`migrate.py`) for transitioning existing umbrella-managed metrics deployments to the `stable-2026a` Component profile (`prepare`, `handoff [--undo]`, `finalize`). See [`docs/migrations/components.md`](../docs/migrations/components.md).

---

## Backward Compatibility & Forwarding Wrappers

For backward compatibility with existing developer scripts and agent skill definitions, top-level forwarding wrappers and repository-level utilities are maintained directly in `tools/`:
- **Repository Entrypoints & Hooks**: Tools invoked directly by git hooks or top-level entrypoints (`analyze_diff_size.py`, `dev_env.py`, `run_affected_tests.py`) remain at the `tools/` root for immediate access.
  - [`run_affected_tests.py`](run_affected_tests.py): Runs the Bazel unit tests affected by local changes, scoped like CI (see [Running Affected Unit Tests](../docs/developer.md#running-affected-unit-tests)).
- **Forwarding Wrappers**: Executing `./tools/<script>.py` or `python tools/<script>.py` transparently delegates to the corresponding modular script in `tools/<subpackage>/<script>.py`.
- **Bazel Aliases**: Bazel targets under `//tools:...` alias to their respective `//tools/<subpackage>:...` targets.

---

## Testing

Run all unit tests across all tools subpackages:

```bash
bazel test //tools/...
```
