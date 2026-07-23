# Local Feature Development Workflow

Guidelines for local feature development, Bazel package target discovery, unit testing, coverage reports, and doc sync.

---

## 1. Feature Development Lifecycle
- **Branch Naming**: Ensure active branch follows `<prefix>/<kebab-case-description>` (e.g. `feat/streaming-json-decoder`).
- **Target Discovery**: Locate affected packages and test targets using Bazel query:
  ```bash
  bazel query "tests(//pkg/auth/...)"
  ```
- **Hermetic Implementation**: Write clean, modular implementation adhering to SRP (20-30 line function limit) and default immutability.

## 2. Unit Testing & Coverage Protocol
- **1-to-1 Unit Test Cohesion**: Ensure every new/modified source file has a dedicated `_test` file (e.g. `decoder.go` -> `decoder_test.go`).
- **Execute Affected Tests**: Run affected unit test suites via:
  ```bash
  python3 ./tools/run_affected_tests.py
  # Or directly via Bazel:
  bazel test //pkg/auth/...
  ```
- **Measure Coverage**: Check lcov package coverage to ensure no untested branches remain:
  ```bash
  bazel coverage --combined_report=lcov //pkg/auth/...
  ```

## 3. Pre-Commit Formatting & Contract Sync
- **Pre-Commit Hook Execution**: Run `./.githooks/pre-commit && git add -u` prior to committing.
- **Documentation Synchronization**: Update relevant `README.md`, specs, and API contracts in the exact same PR when system behavior changes.
