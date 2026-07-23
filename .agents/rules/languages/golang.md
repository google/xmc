# Go (Golang) Guidelines

Coding standards and conventions for Go development across the codebase.

---

## 1. Style & Conventions
- **Idiomatic Go Style Guide**: Strictly follow standard Go conventions and Effective Go recommendations.
- **Simplicity & Readability**: Keep Go code clean, explicit, and idiomatic. Avoid unnecessary reflection or overly complex abstractions.

## 2. Interface Design & Errors
- **Small, Focused Interfaces**: Keep interfaces small (1–3 methods) and define them where consumed rather than where implemented.
- **Accept Interfaces, Return Structs**: Functions should accept interface parameters and return concrete struct types.
- **Explicit Error Checks**: Never ignore returned errors. Handle immediately using guard clauses.
- **Lowercase `%w` Error Wrapping**: Wrap errors with context: `fmt.Errorf("failed to execute step: %w", err)`. Start error strings with lowercase and omit trailing periods.

## 3. Concurrency Safety
- **Goroutine Lifecycle Management**: Always govern goroutine lifecycles using `context.Context` cancellation, `sync.WaitGroup`, or channels to prevent goroutine leaks.
- **Data Race Prevention**: Protect shared mutable state with `sync.Mutex` or `sync.RWMutex`. Verify concurrent routines with the `-race` detector enabled.

## 4. Table-Driven Testing & Baseline Fixtures
- **Table-Driven Tests**: Standardize unit test suites using Go table-driven patterns (`tests := []struct{...}`). Execute subtests via `t.Run(tt.name, ...)`.
- **Baseline Fixtures & `proto.Modify`**: Avoid copy-pasting monolithic struct literals across test cases. Define a canonical baseline fixture and use `proto.Modify` for Protobuf messages or shallow copies/local mutation helpers for standard structs.
- **Centralize Common Test Utilities**: Shared test and proto utilities must be centralized in global/shared packages (e.g. `proto.Modify` in `//internal/compat/proto`).

## 5. Go Dependencies & Bazel
- **Bazel Proto Target Rules**: Inspect target proto package `BUILD.bazel` to verify exact `go_proto_library` target names (typically ending in `_go_proto`) before adding Bazel dependencies.
