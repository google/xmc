# Canonical Component Profiles (`examples/components/profiles/`)

This directory provides canonical reference profiles and Kubernetes Component CRD manifests used for integration testing, demonstration, and continuous verification with [`component-linter`](../../../cmd/component-linter/README.md). For the production release profile shipping `redpanda`, `clickhouse`, and `ingestion-gateway`, see [`deploy/profiles/stable-2026a/`](../../../deploy/profiles/stable-2026a/README.md).

---

## Profile Structure: `default/`

The `default/` directory contains a minimal self-contained component profile demonstrating intra-profile dependencies and static validation rules.

### Included Manifests

- **[`storage.yaml`](default/storage.yaml)**: Defines the foundational `storage` component (`CUSTOM_1P_COMPONENT`, version `1.2.0`) providing persistent storage backend services with a 60-second drain timeout.
- **[`logging.yaml`](default/logging.yaml)**: Defines the `logging` component (`CUSTOM_1P_COMPONENT`, version `1.0.0`) which declares a dependency on `storage` with SemVer constraint `^1.0.0`.
- **[`rules.yaml`](default/rules.yaml)**: Defines profile-level static checks and compatibility matrix rules:
  - `maxDrainTimeoutSeconds: 300` boundary check.
  - Asserting that `logging` requires `storage` version `^1.0.0`.
  - Asserting platform Kubernetes version boundaries for `storage` (`>= 1.28.0 <= 1.32.0`).

---

## Local Verification

Validate the canonical profile locally using either the multi-profile discovery runner or the standalone `component-linter` binary:

### Via Discovery Runner (Recommended)

```bash
bazel run //tools/components:run_linter -- --scan-roots examples/components/profiles/default --strict
```

### Via Standalone `component-linter`

```bash
bazel run //cmd/component-linter:component-linter -- \
  --manifests-dir=examples/components/profiles/default \
  --rules-config=examples/components/profiles/default/rules.yaml \
  --output=text
```
