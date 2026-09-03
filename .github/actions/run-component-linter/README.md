# Run Component Manifest Linter Action (`run-component-linter`)

A composite GitHub Action that executes offline static analysis and pre-flight validation on Component CRD manifests and profile compatibility rules before pull requests are merged.

---

## 1. Overview

This action compiles the [`component-linter`](../../../cmd/component-linter/README.md) CLI binary using Go in milliseconds and executes the repository-wide discovery runner [`tools/components/run_linter.py`](../../../tools/components/run_linter.py) across all configured component profile directories (see [`tools/components/README.md`](../../../tools/components/README.md)).

```mermaid
flowchart LR
    PR["Pull Request / Push"] --> Filter["Path Filter Check\n(dorny/paths-filter)"]
    Filter -->|"No changes"| Skip["Skip (Log Notice)"]
    Filter -->|"Changes detected"| GoBuild["Go Build\n(cmd/component-linter)"]
    GoBuild --> Runner["run_linter.py\n(Discovers & validates profiles)"]
    Runner --> Result{"Valid?"}
    Result -->|"Yes"| Pass["✅ CI Green"]
    Result -->|"No"| Fail["❌ CI Fail (Exit 1)"]
```

---

## 2. Inputs

| Input | Required | Default | Description |
| :--- | :---: | :--- | :--- |
| `scan-roots` | No | `deploy/profiles deploy/components examples/components/profiles` | Space-separated list of root directories to scan for component profiles. |
| `output-format` | No | `text` | Output format: `text` (console summary) or `json` (machine-readable diagnostics). |
| `strict` | No | `false` | When `true`, fails if no component profile targets are found across the scan roots. |

---

## 3. Key Features

- **Selective Execution (Path Filtering)**: Only triggers when relevant paths change (`deploy/profiles/**`, `deploy/components/**`, `examples/components/**`, `api/protos/components/**`, `pkg/components/**`, `cmd/component-linter/**`, `tools/components/**`, `.github/actions/run-component-linter/**`, `.github/workflows/lint.yml`).
- **Sub-Second Go Compilation**: Compiles the standalone linter CLI via `go build` directly in `~0.2s` without heavy Bazel or C++ toolchain overhead.
- **Isolated Binary Execution**: Emits compiled binaries to an isolated `${RUNNER_TEMP}` location, avoiding Git workspace contamination and runner volume leakage.

---

## 4. Workflow Usage Example

Integrated into [`.github/workflows/lint.yml`](../../workflows/lint.yml) (`Code Quality & Linting`):

```yaml
      # 5. Component Manifests & Profiles Linting
      - name: 'Run Component Manifest Linter'
        uses: './.github/actions/run-component-linter'
        with:
          strict: 'true'
```
