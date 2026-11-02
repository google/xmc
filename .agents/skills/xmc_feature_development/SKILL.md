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
   - Inspect the CI-equivalent affected test scope (including reverse dependencies and any UI/Helm suites under `other_suites`) without running tests:
     ```bash
     python3 ./tools/run_affected_tests.py --dry-run
     ```
   - To list affected `go_binary` targets for a specific package when needed:
     ```bash
     bazel query "kind(go_binary, rdeps(//..., //pkg/auth/oauth2/...))"
     ```

---

## 2. Code Implementation & Clean Code Design

1. **Apply SRP & KISS**:
   - Keep functions focused and under the 20–30 line target cap.
   - Use early return guard clauses at the top of functions; eliminate redundant `else` blocks after returns.
   - Prefer default immutability (`const` in TypeScript, value receivers or immutable structs where applicable).
2. **Decouple Mocks & Baseline Test Fixtures**:
   - Centralize shared helpers in global/shared packages.
   - Define baseline test fixtures and express test cases as concise deltas (e.g., using `prototest.Modify`).
3. **Open-Source Code TODOs**:
   - Ensure inline `TODO` comments in public packages use `TODO:` or public issue links `TODO(#123)`, avoiding internal bug IDs.

---

## 3. Test Execution via Affected Tests Tool

1. **Run Affected Tests Script**:
   Execute the automated affected tests runner to resolve all changed files since `origin/main` (including uncommitted and untracked files) via `resolve_test_scope.py` and run `bazel test` on affected targets with CI-equivalent flags:
   ```bash
   python3 ./tools/run_affected_tests.py
   ```
   - **Output & Exit Codes**: stdout emits structured JSON (`changed_files`, `backend_mode`, `reason`, `bazel_targets`, `other_suites`, `passed_targets`); Bazel output streams to stderr. Exits `0` when tests pass (or dry-run / nothing to run), `1` on test failures or handled errors (with failed targets and log paths in `details`), and `2` on unexpected errors.
   - **Spanner Emulator**: Pre-flights `SPANNER_EMULATOR_HOST` (default `localhost:9010`) and forwards non-default addresses via `--test_env`. Pass `--skip-emulator-check` when affected targets do not use Spanner.
   - **Other Suites**: When `other_suites` lists the UI or Helm chart suite, run those suites before opening a PR.
2. **Targeted Package Iteration (During Editing)**:
   ```bash
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
   - Locate the coverage output at `bazel-out/_coverage/_coverage_report.dat`.
   - Verify that 100% of newly added or modified lines of business logic are covered by unit tests.
   - Ensure 1-to-1 test file cohesion (each source file must have a dedicated `*_test.go` or `*_test.py` file).

---

## 5. Pre-Commit Verification & Staging

1. **Stage, Then Run Pre-Commit Hooks**:
   Before committing code changes, stage them and run the repository pre-commit script, which formats files, auto-restages formatter fixes to fully staged files, runs linters, and executes `run_affected_tests.py --skip-emulator-check`:
   ```bash
   git add <changed_paths>
   ./.githooks/pre-commit
   ```
2. **Verify Linter & Affected Test Status**:
   Ensure all linters and affected unit tests pass without errors or warnings.
3. **Proceed to PR Creation**:
   Refer to the [XMC Create PR Skill](../xmc_create_pr/SKILL.md) to format the commit message and submit a Pull Request.
