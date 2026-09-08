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
| `--output` | `-o` | `stdout` | Path to write output JSON file |

---

## 2. Output Schema

The tool emits structured JSON metadata:

```json
{
  "metadata": {
    "base_ref": "0.5.0",
    "target_ref": "0.6.0",
    "commit_count": 42,
    "pr_count": 39
  },
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

## 3. Package Architecture

- [`extract_release_metadata.py`](extract_release_metadata.py): CLI entrypoint orchestrating git extraction and output generation.
- [`git_ops.py`](git_ops.py): Subprocess execution wrappers for `git log`, `git describe`, and `git show` with error handling.
- [`models.py`](models.py): Strongly-typed dataclasses for release artifacts (`CommitInfo`).

---

## 4. Testing

Run unit tests via Bazel:

```bash
bazel test //tools/release:extract_release_metadata_test //tools/release:git_ops_test
```
