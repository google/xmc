---
name: XMC Feature Development
description: Guides agents through feature development, code refactoring, Bazel test matrix execution, coverage measurement, and pre-commit verification.
---

# Feature Development & Refactoring Playbook

This skill provides step-by-step procedures for implementing new features, refactoring existing code, running test matrices, measuring coverage, and preparing changes for PR submission.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [Core Engineering Standards](../../rules/core_engineering.md) and [Skills & Automation Guidelines](../../rules/skills_and_automation.md).

---

## Guidelines for Using CLI Tools

When invoking helper tools under `tools/` (e.g., `run_affected_tests.py`):
1. **Do not read tool source code**: Do not open or read the Python source code of helper tools. Refer to [Skills & Automation Guidelines](../../rules/skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints).
2. **Use `--help`**: Run tools with `--help` to inspect parameters and usage.
3. **Parse Structured Output & Handle Errors**: All tools output structured JSON (exit code 0 for success, 1 for handled error, 2 for unhandled error).
4. **Shell & Payload Safety**: Execute standalone `.py` script files or pass arguments via script parameters. Avoid double-quoted inline commands (`python3 -c "..."`).

---

## 1. Requirements Scoping & Package Identification

1. **Scope Task & Identify Modified Files**:
   - Analyze user request and identify target packages/directories (e.g., `pkg/auth/oauth2`, `cmd/xid_service`).
   - Identify existing 1-to-1 unit test files (e.g., `handlers_test.go` for `handlers.go`). If missing, plan to create a dedicated unit test file.
2. **Dependency & Target Discovery**:
   - Use Bazel query to list affected binaries and tests:
     ```bash
     bazel query "rdeps(//..., //pkg/auth/oauth2/...)"
     ```

---

## 2. Code Implementation & Clean Code Design

1. **Apply SRP & KISS**:
   - Keep functions focused and under the 20–30 line target cap.
   - Use early return guard clauses at the top of functions; eliminate redundant `else` blocks after returns.
   - Prefer default immutability (`const` in TypeScript, value receivers or immutable structs where applicable).
2. **Decouple Mocks & Baseline Test Fixtures**:
   - Centralize shared helpers in global/shared packages.
   - Define baseline test fixtures and express test cases as concise deltas (e.g., using `proto.Modify`).
3. **Open-Source Code TODOs**:
   - Ensure inline `TODO` comments in public packages use `TODO:` or public issue links `TODO(#123)`, avoiding internal bug IDs.

---

## 3. Test Execution via Affected Tests Tool

1. **Run Affected Tests Script**:
   Execute the automated affected tests runner to run all unit tests associated with changed packages and their reverse dependencies:
   ```bash
   python3 ./tools/run_affected_tests.py
   ```
2. **Run Package Unit Tests Directly**:
   ```bash
   # Go package tests
   go test -v ./pkg/auth/oauth2/...

   # Bazel targeted package test
   bazel test //pkg/auth/oauth2/...
   ```

---

## 4. Code Coverage Measurement

1. **Execute Bazel Coverage**:
   Run coverage evaluation for the target package:
   ```bash
   bazel coverage --combined_report=lcov //pkg/auth/oauth2/...
   ```
2. **Parse & Verify Coverage**:
   - Execute `./tools/parse_coverage_report.py` directly (it automatically reads `bazel-out/_coverage/_coverage_report.dat`):
     ```bash
     ./tools/parse_coverage_report.py --package //pkg/auth/oauth2/...
     ```
   - Verify that 100% of newly added or modified lines of business logic are covered by unit tests, checking the `uncovered_line_numbers` array in the JSON output.
   - Enforce minimum coverage thresholds where applicable (e.g. `--min-coverage 80.0`).
   - Do not perform manual file searches (`find_by_name`) or poll background task status; rely on `./tools/parse_coverage_report.py` JSON output directly.
   - Ensure 1-to-1 test file cohesion (each source file must have a dedicated `*_test.go` or `*_test.py` file).



---

## 5. Pre-Commit Verification & Staging

1. **Run Pre-Commit Hooks**:
   Before committing code changes, run the repository pre-commit script to format files and stage clean edits:
   ```bash
   ./.githooks/pre-commit && git add .
   ```
2. **Verify Linter Status**:
   Ensure all linters pass without errors or warnings.
3. **Proceed to PR Creation**:
   Refer to the [XMC Create PR Skill](../xmc_create_pr/SKILL.md) to format the commit message and submit a Pull Request.
