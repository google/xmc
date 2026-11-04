# Release Automation & Metadata Tooling (`tools/release/`)

The `tools/release/` package provides developer tooling for extracting git commit history, analyzing repository changes, and generating structured release metadata across XMC releases.

---

## 1. Release Metadata Extractor (`extract_release_metadata.py`)

`extract_release_metadata.py` extracts structured git commit metadata between two git references (tags, branches, or commit hashes), parses conventional commit headers, and outputs JSON formatted metadata.

### CLI Usage

```bash
# Extract commits between a previous release tag and current HEAD
python3 tools/release/extract_release_metadata.py --base-ref 0.5.0 --target-ref HEAD

# Auto-detect the previous tag before target-ref
python3 tools/release/extract_release_metadata.py --target-ref 0.6.0

# Write metadata directly to a JSON file
python3 tools/release/extract_release_metadata.py \
  --base-ref 0.5.0 \
  --target-ref 0.6.0 \
  --output /tmp/release_metadata.json
```

### CLI Arguments Reference

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--target-ref` | | `HEAD` | Target git commit, tag, or branch |
| `--base-ref` | | `""` (auto-detected) | Base git tag to diff against (defaults to previous tag before `target-ref`) |
| `--repo-dir` | | `.` | Root path to git repository |
| `--approved-flags` | | `[]` | Feature flags approved for production deployment in this release |
| `--output` | `-o` | `stdout` | Path to write output JSON file |

---

## 2. Dynamic Feature Flag Discovery & Transitions

The tool analyzes Cluster Toolkit (CTK v2) and Helm deployment configuration templates (`deploy/ctk_v2/` and `deploy/helm/values.yaml`):

- **Feature Flag Discovery**: Identifies boolean feature flags matching prefixes/suffixes (`enable_*`, `use_*`, `*_enabled`, `*.enabled`, `*_deployment_enabled`, `Deployment.enabled`).
- **Flag Transitions**: Diffs configuration states between `base_ref` and `target_ref`, detecting when flags are introduced, flipped, or promoted to default `true` out-of-the-box.
- **Gated Subsystem Matrix**: Emits a deterministic status matrix for all discovered flags (`enabled_by_default`, `approved_preview_toggle`, or `experimental_preview_toggle`).

---

## 3. Semantic Version Recommendation Engine (`semver_engine.py`)

The tool calculates a deterministic semantic version bump recommendation based on changes between `base_ref` and `target_ref`:

- **Major (`X.0.0`)**: Breaking changes detected in commit subjects (`type!:`) or body descriptions (`BREAKING CHANGE:`).
- **Minor (`0.X.0`)**: Behind-the-flag features promoted to default `true`, flags explicitly approved via `--approved-flags`, or user-facing feature commits (`feat`).
- **Patch (`0.0.X`)**: User-facing bug fixes (`fix`), database schema migrations, or general developer velocity and infrastructure updates.
- **None**: No changes between references.

---

## 4. Output Schema

The tool emits structured JSON metadata:

```json
{
  "metadata": {
    "base_ref": "0.5.0",
    "target_ref": "0.6.0",
    "commit_count": 42,
    "pr_count": 39,
    "approved_flags": ["rod_deployment_enabled"]
  },
  "recommended_version": {
    "current_version": "0.5.0",
    "recommended_version": "0.6.0",
    "recommended_bump": "minor",
    "has_breaking_changes": false,
    "has_promotions": true,
    "has_approved_features": true,
    "has_ootb_features": true,
    "has_ootb_fixes": false,
    "has_migrations": false,
    "rationale": [
      "Feature flags promoted to default true.",
      "Behind-the-flag features approved for deployment.",
      "New out-of-the-box user-facing features introduced."
    ]
  },
  "flag_transitions": [
    {
      "flag_name": "enable_components",
      "source_file": "deploy/ctk_v2/xmanager-cloud.yaml",
      "base_value": false,
      "target_value": true,
      "is_flip": true,
      "is_promotion_to_default": true,
      "description": "Promoted enable_components to default true (enabled out-of-the-box)"
    }
  ],
  "gated_subsystems_status": [
    {
      "subsystem": "Components",
      "flag_name": "enable_components",
      "status": "enabled_by_default",
      "ctk_value": true,
      "helm_value": null
    }
  ],
  "raw_commits": [
    {
      "commit_hash": "a1b2c3d4e5f6...",
      "short_hash": "a1b2c3d",
      "author": "Google Engineer",
      "date": "2026-08-21",
      "subject": "feat(ui): add live queue picker (#436)",
      "commit_type": "feat",
      "scope": "ui",
      "description": "add live queue picker",
      "pr_number": 436,
      "files_changed": [
        "cmd/ui/frontend/src/app/queue-picker.ts"
      ]
    }
  ]
}
```

---

## 5. Release Publisher CLI (`publish_release.py`)

`publish_release.py` creates an annotated git tag, pushes it to remote origin, and publishes the official release on GitHub via the `gh` CLI.

### CLI Usage

```bash
# Publish release tag and notes on GitHub
python3 tools/release/publish_release.py \
  --tag 0.6.0 \
  --notes-file /tmp/release_notes_v0.6.0.md

# Publish with custom release title
python3 tools/release/publish_release.py \
  --tag 0.6.0 \
  --title "XMC 0.6.0 - Enhanced Telemetry and Dashboard" \
  --notes-file /tmp/release_notes_v0.6.0.md

# Validate release tag and notes locally without creating tags or publishing (dry run)
python3 tools/release/publish_release.py \
  --tag 0.6.0 \
  --notes-file /tmp/release_notes_v0.6.0.md \
  --dry-run

# Publish release notes when the git tag already exists on the remote repository
python3 tools/release/publish_release.py \
  --tag 0.6.0 \
  --notes-file /tmp/release_notes_v0.6.0.md \
  --skip-push
```

### CLI Arguments Reference

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--tag` | `-t` | *(required)* | Release semantic version tag (e.g. `0.6.0` or `v0.6.0`) |
| `--notes-file` | `-f` | *(required)* | Path to markdown file containing formatted release notes |
| `--title` | | `Release <tag>` | Release title on GitHub |
| `--target-commit` | `-c` | `HEAD` | Target commit SHA, branch, or tag |
| `--repo-dir` | `-r` | `.` | Path to git repository root |
| `--remote` | | `origin` | Git remote name |
| `--dry-run` | | `false` | Validate inputs, commit resolution, and tag availability without creating tags or publishing |
| `--skip-push` | | `false` | Skip pushing local tag to remote (passes `--verify-tag` to `gh release create`, requiring the tag to already exist on remote) |
| `--draft` | | `false` | Create release as draft on GitHub |
| `--prerelease` | | `false` | Mark release as prerelease on GitHub |
| `--force` | | `false` | Overwrite existing local or remote tag |

### Headless CI & REST API Fallback

In headless environments where the `gh` CLI is not installed, `publish_release.py` automatically falls back to creating the release using the GitHub REST API (`POST /repos/{owner}/{repo}/releases`). Ensure `GITHUB_TOKEN` or `GH_TOKEN` is exported in the environment with repo write permissions:

```bash
export GITHUB_TOKEN="ghp_your_personal_access_token"
python3 tools/release/publish_release.py --tag 0.6.0 --notes-file /tmp/release_notes_v0.6.0.md
```

---

## 6. Workflow Dispatch & Monitoring (`dispatch_and_watch.py`)

`dispatch_and_watch.py` is a streamlined CLI helper tool that triggers GitHub Actions workflows (optionally forwarding repeatable `--input KEY=VALUE` parameters and validating `examples_filter` against `matrix.example.id`), auto-fetches `origin/<ref>` to resolve `FETCH_HEAD` when `--target-sha` is omitted, polls for run registration with commit SHA verification, watches execution to completion, extracts GitHub Pages URLs, and updates state files.

### CLI Usage

```bash
# Dispatch a nightly deployment and watch outcome
python3 tools/release/dispatch_and_watch.py \
  --workflow nightly-build.yml \
  --target-sha "$(git rev-parse origin/main)" \
  --state-file /tmp/release_state.json

# Reuse an existing successful run matching target SHA if already executed
python3 tools/release/dispatch_and_watch.py \
  --workflow nightly-build.yml \
  --target-sha "$(git rev-parse origin/main)" \
  --find-existing \
  --state-file /tmp/release_state.json

# Dispatch a branch run with workflow_dispatch inputs (fetches origin/<ref> automatically)
python3 tools/release/dispatch_and_watch.py \
  --workflow nightly-examples-e2e.yml \
  --ref "fix/nightly-<slug>" \
  --input "examples_filter=resource_dashboard_ui" \
  --state-file /tmp/nightly_verify_state.json
```

### CLI Arguments Reference

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--workflow` | `-w` | *(required)* | Workflow filename (e.g. `nightly-build.yml`) |
| `--target-sha` | `-s`, `--sha` | `""` | Expected git commit SHA (if omitted on dispatch, fetches `origin/<ref>` and resolves `FETCH_HEAD`) |
| `--ref` | `-r` | `main` | Target git branch or tag name on `origin` (without `origin/` prefix) |
| `--find-existing` | | `false` | Check for and reuse an existing successful run matching `target_sha` (cannot be combined with `--input`) |
| `--input` | | `[]` | Repeatable `KEY=VALUE` `workflow_dispatch` input forwarded as `gh workflow run -f KEY=VALUE` (`examples_filter` is validated pre-flight against `matrix.example.id`) |
| `--state-file` | | `None` | Path to JSON state file to persist run metadata and outcomes |
| `--poll-interval` | | `5.0` | Seconds to wait between polling attempts for run registration |
| `--poll-attempts` | | `12` | Maximum number of polling attempts before timing out |
| `--repo-dir` | | `.` | Root directory of the repository |

---

## 7. Package Architecture

- [`dispatch_and_watch.py`](dispatch_and_watch.py): CLI tool to dispatch GitHub Actions release workflows, poll for correlation, watch execution outcomes, and persist release state.
- [`extract_release_metadata.py`](extract_release_metadata.py): CLI entrypoint orchestrating git extraction, feature flag analysis, SemVer recommendation, and output generation.
- [`feature_flags.py`](feature_flags.py): Discovers feature toggles from CTK and Helm manifests and computes flag transitions between releases.
- [`git_ops.py`](git_ops.py): Subprocess execution wrappers for `git log`, `git describe`, `git remote`, `git show`, and annotated tag lifecycle operations (`verify_local_tag`, `verify_remote_tag`, `create_git_tag`, `push_git_tag`).
- [`github_release_client.py`](github_release_client.py): GitHub release creation client transports (`publish_via_gh_cli` and headless REST API fallback `publish_via_rest_api`).
- [`models.py`](models.py): Strongly-typed dataclasses and TypedDicts for release metadata, commits, flag transitions, and publication results.
- [`publish_release.py`](publish_release.py): CLI entrypoint orchestrating pre-flight validation, git tag lifecycle, and GitHub release publication.
- [`semver_engine.py`](semver_engine.py): Semantic version calculation and release increment recommendation engine.
- [`yaml_parser.py`](yaml_parser.py): Robust YAML AST parsing and indentation fallback scanner for extracting nested boolean configurations.

---

## 8. Testing

Run unit tests via Bazel:

```bash
bazel test //tools/release:all
```

Or execute individual test targets:

```bash
bazel test //tools/release:dispatch_and_watch_test \
  //tools/release:extract_release_metadata_test \
  //tools/release:feature_flags_test \
  //tools/release:git_ops_test \
  //tools/release:github_release_client_test \
  //tools/release:publish_release_test \
  //tools/release:semver_engine_test \
  //tools/release:yaml_parser_test
```

