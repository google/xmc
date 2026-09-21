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

## 3. Output Schema

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

## 4. Package Architecture

- [`extract_release_metadata.py`](extract_release_metadata.py): CLI entrypoint orchestrating git extraction, feature flag analysis, and output generation.
- [`feature_flags.py`](feature_flags.py): Discovers feature toggles from CTK and Helm manifests and computes flag transitions between releases.
- [`git_ops.py`](git_ops.py): Subprocess execution wrappers for `git log`, `git describe`, and `git show` with error handling.
- [`models.py`](models.py): Strongly-typed dataclasses and TypedDicts for release metadata, commits, and flag transitions.
- [`yaml_parser.py`](yaml_parser.py): Robust YAML AST parsing and indentation fallback scanner for extracting nested boolean configurations.

---

## 5. Testing

Run unit tests via Bazel:

```bash
bazel test //tools/release:extract_release_metadata_test \
  //tools/release:feature_flags_test \
  //tools/release:git_ops_test \
  //tools/release:yaml_parser_test
```
