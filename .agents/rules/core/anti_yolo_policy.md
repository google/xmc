# Anti-YOLO Execution Protocol & Safety Policy

This document defines the mandatory 4-phase execution lifecycle and gatekeeper safety requirements for all high-risk operations.

---

## 1. Mandatory 4-Phase Execution Lifecycle

Every non-trivial engineering task or modification MUST strictly follow this linear 4-phase lifecycle:

1. **Phase 1: Plan**
   - Inspect files, discover dependencies, and formulate a clear, step-by-step implementation plan.
   - Identify all affected targets, binaries, and documentation files.
2. **Phase 2: Present Plan**
   - Present the plan to the user or record it in structured workspace notes (`_worker_notes/PLAN.md`).
   - If the task introduces breaking API/contract changes, infrastructure teardowns, or public schema edits, **obtain explicit user approval before proceeding**.
3. **Phase 3: Execute**
   - Apply edits incrementally. Adhere strictly to SRP, KISS, and default immutability.
   - Run formatting linters (`./.githooks/pre-commit` or standard linters) before committing.
4. **Phase 4: Verify**
   - Run unit/integration tests (`./tools/run_affected_tests.py` or Bazel test targets).
   - Verify coverage requirements and test isolation.

---

## 2. Gatekeeper Requirements for High-Risk Actions

The following actions require explicit user confirmation or automated safety checks before execution:

- **Public API & Contract Changes**: Any modification to Protobuf schemas, CLI flags, or public API signatures.
- **Infrastructure Destruction / Teardown**: Running `gcluster destroy`, deleting Cloud Spanner instances/backups, or removing VPC networks.
- **Force-Push Prohibition**: Using `git push --force` or `git push --force-with-lease` on shared or active PR branches is strictly forbidden.
- **Direct Mocks in Production**: Adding conditional mock branches (`if (useMock)`) into production code.
- **Unverified PR Resolution**: Marking PR review tasks complete without running `tools/verify_pr_comment_resolution.py`.
