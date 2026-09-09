# Go Test Determinism & State Leakage Checker (`check_go_determinism`)

An ultra-fast (<50ms), zero-dependency static analyzer that audits Go unit and integration test files for non-deterministic patterns and test environment pollution.

---

## 1. Motivation & Background

During a comprehensive audit of code reviews across 300 pull requests in `google/xmc`, non-determinism and test flakiness emerged as major friction points:
- **Environment Variable Leakage**: Tests calling `os.Setenv(...)` without cleanup or relying on fragile manual restoration polluted process environment state, causing subsequent tests in the same binary runner to fail unpredictably.
- **Excessive Sleep Delays**: Tests using arbitrary second-level wall-clock sleeps (e.g. 15–22s sleeps in `cmd/health_service/server_test.go`) slowed down CI suites and still suffered from timing jitter under heavy CPU contention.

### Why a Custom AST Analyzer Instead of `golangci-lint` Linters?
Standard linters like `tenv` and `forbidigo` rely on Go's compiler typechecker (`go/types`). In Bazel monorepos like XMC:
- Generated Protobuf bindings (`.pb.go`) and Bazel runtime sandboxes are generated during Bazel build phases and are not checked into source control.
- Running typecheck-dependent linters on the host outside Bazel causes dozens of false `(typecheck)` failure errors.
- `check_go_determinism` uses Go's standard library `go/parser` and `go/ast` to inspect syntax trees purely without invoking the typechecker. It executes in **0.05s** across 265+ test files hermetically.

---

## 2. Rules Enforced

### Rule 1: Prevent Environment Mutation (`os.Setenv`)
- **Violation**: Calling `os.Setenv(key, val)` in any `*_test.go` file.
- **Remediation**: Use `t.Setenv(key, val)` which automatically restores the previous environment variable state when the test and its subtests complete.
- **Message**:
  ```
  path/to/file_test.go:42: os.Setenv used in test. Use t.Setenv(...) to guarantee cleanup and prevent environment pollution.
  ```

### Rule 2: Prevent Arbitrary Second-Level Sleep (`time.Sleep`)
- **Violation**: Calling `time.Sleep(...)` with a duration of `time.Second`, `time.Minute`, or `time.Hour` (including arithmetic expressions like `5 * time.Second`, `time.Minute / 2`, or parenthesized/type-converted expressions).
- **Remediation**:
  1. Inject millisecond TTLs into configuration structs (e.g., 500ms cache TTL, 300ms rate limits).
  2. Use `testing/synctest` for synthetic virtualized time without real clock progression.
  3. Use polling helpers (e.g., `k8s.io/apimachinery/pkg/util/wait.PollUntilContextTimeout`).
- **Message**:
  ```
  path/to/file_test.go:85: arbitrary second-level time.Sleep in test. Inject millisecond TTLs, use synctest, or use wait.Poll.
  ```

---

## 3. Suppression Directives

When modifying global environment state is genuinely unavoidable (such as setting up baseline environment variables in `TestMain` before test suites initialize) or when a deliberate timing delay is required, suppress violations using inline or block comments:

- `// nolint:determinism`: Suppresses both `os.Setenv` and `time.Sleep` violations on the matching or subsequent line.
- `// nolint:sleep`: Specifically suppresses `time.Sleep` violations.

### Examples:
```go
// nolint:determinism // TestMain sets process-wide default before tests run
os.Setenv("APP_ENV", "test")

time.Sleep(2 * time.Second) // nolint:sleep // waiting for external process boot
```

---

## 4. Usage

### Command Line
Run across the entire repository:
```bash
go run ./tools/lint/check_go_determinism
```

Run on specific files or packages:
```bash
go run ./tools/lint/check_go_determinism ./pkg/auth/... ./cmd/health_service/server_test.go
```

### Bazel Test
Execute the comprehensive unit test suite:
```bash
bazel test //tools/lint/check_go_determinism:check_go_determinism_test
```

### Pre-commit Hook Integration
The checker is automatically executed on all staged Go files during `.githooks/pre-commit` and in GitHub Actions CI (`.github/workflows/lint.yml`).
