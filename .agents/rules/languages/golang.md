# Go (Golang) Guidelines

This document defines coding standards and conventions for Go development across the codebase.

---

## 1. Style & Conventions
- **Idiomatic Go Style Guide**: Strictly follow standard Go conventions and Effective Go style recommendations.
- **Simplicity & Readability**: Keep Go code clean, explicit, and idiomatic. Avoid unnecessary reflection or overly abstract abstractions.

## 2. Interface Design
- **Small, Focused Interfaces**: Keep interfaces small and decoupled (typically 1–3 methods). Define interfaces where they are consumed rather than where they are implemented.
- **Accept Interfaces, Return Structs**: Functions and constructors should accept interface parameters and return concrete struct types wherever practical.

## 3. Error Handling & Formatting
- **Explicit Error Checks**: Never ignore returned errors. Handle error branches immediately using early return guard clauses.
- **Proper Error Wrapping**: Wrap lower-level errors with operational context using `fmt.Errorf("failed to execute step: %w", err)` to preserve causality chains.
- **Formatting Rules**: Error strings must begin with a lowercase letter and must omit trailing punctuation.

## 4. Concurrency Safety
- **Goroutine Lifecycle Management**: Always govern goroutine lifecycles using `context.Context` cancellation, `sync.WaitGroup`, or worker channels to prevent goroutine leaks.
- **Data Race Prevention**: Protect shared mutable state with `sync.Mutex` or `sync.RWMutex`. Verify concurrent routines with the `-race` detector enabled.

## 5. Table-Driven Testing
- **Table-Driven Tests**: Standardize unit test suites using Go table-driven patterns (`tests := []struct{ name string; ... }{...}`).
- **Subtest Execution**: Execute subtests using `t.Run(tt.name, func(t *testing.T) { ... })` to ensure clear failure reporting and isolated execution.
- **Baseline Fixtures & Targeted Mutations (`proto.Modify`)**: Follow baseline fixture policies in [Core Engineering Standards](../core_engineering.md#4-high-fidelity-test-doubles--decoupling). In table-driven tests, define a canonical baseline fixture (`baseReq`) and use `proto.Modify` for Protobuf mutations or shallow copies/helpers for Go structs to keep test tables DRY and readable.
- **Centralize Common Test Utilities**: Shared test and proto utilities must be centralized in global packages (e.g., `proto.Modify` in `//internal/compat/proto`) per [Core Engineering Standards](../core_engineering.md#4-high-fidelity-test-doubles--decoupling).

## 6. Go Dependencies / Bazel
- Always inspect proto package `BUILD.bazel` to verify exact `go_proto_library` target name (typically ending in `_go_proto`) before adding Bazel dependencies.

## 7. Go CLI Entrypoints & Exit Cleanups
- **Graceful Execution Pattern**: NEVER call `os.Exit()` directly inside `main()` when `defer` statements (e.g. database handles, socket cleanups) are registered. Standardize on the `os.Exit(run())` pattern where `run() int` returns the exit code after all deferred cleanups execute.
- **Zero-Value Flag Defaults for Environment Fallbacks**: When CLI flags override environment variable fallbacks, set CLI flag defaults to zero-values (`0`, `""`) to accurately distinguish explicit CLI overrides from environment variable defaults.

## 8. Go Map Safety, Mutability & Pointer Isolation
- **Map Pointer Isolation**: NEVER assign the same map reference to multiple struct fields or nested objects (e.g., `JobTemplateSpec.ObjectMeta.Labels` and `PodTemplateSpec.ObjectMeta.Labels`). Construct a shallow copy for each sub-struct:
  ```go
  podLabels := make(map[string]string, len(labels))
  for k, v := range labels {
      podLabels[k] = v
  }
  ```
- **Pre-allocating Map Capacity**: Specify initial capacity `make(map[K]V, capacity)` whenever map entry counts are known to prevent runtime re-allocations.
- **Deterministic Map Iteration**: Map iteration in Go is non-deterministic. Always sort slice outputs derived from map keys prior to serialization or return.

## 8. Context Safety in `defer` Cleanups & Error Branches
- **Fresh Cleanup Context**: Deferred cleanup functions (`Teardown`, `Close`, container termination) MUST NOT pass a parent context (`ctx`) that might already be canceled or timed out. Wrap the context creation and cleanup call inside a deferred closure so the timeout begins when cleanup actually starts:
  ```go
  defer func() {
      cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
      defer cancel()
      c.Terminate(cleanupCtx)
  }()
  ```

## 9. Integration Testing & Testcontainers Best Practices
- **Host File Mount Permissions**: Call `os.Chmod(path, 0644)` on temporary host files mounted into Docker containers so non-root container users (e.g. `clickhouse`) have read access.
- **Shared Container Networks**: Multi-container integration tests requiring inter-service resolution MUST create and attach a shared bridge network (`testcontainers.NewDockerNetwork(ctx, ...)`).
- **Bazel Runfiles & Local Tag**: Integration tests reading repo files MUST declare `data` dependencies in `BUILD.bazel`, resolve runfiles using the official `bazel.Runfile(path)` helper from `github.com/bazelbuild/rules_go/go/tools/bazel` (or `os.Getenv("TEST_SRCDIR")`), and set `tags = ["local"]`.
- **Status Error Wrapping Integrity**: Preserve gRPC status error constructors (`status.Error(codes.Internal, ...)` / `status.Errorf(...)` from `google.golang.org/grpc/status` or package-level error variables from `internal/compat/status` / `task/go/status`). Replacing status error wrappers with generic `fmt.Errorf` breaks upstream RPC status code contracts.
- **Compile-Time Interface Assertions**: All concrete repository implementations MUST include explicit compile-time interface assertions:
  ```go
  var _ storage.WorkUnitStore = (*Repository)(nil)
  ```
