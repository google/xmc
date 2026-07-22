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
- **Minimize Test Data Duplication**: In table-driven tests (especially when constructing complex request structs or Protobuf messages), do not copy-paste full struct literals across every test case. Define a canonical baseline fixture (such as `baseReq` or `baseWorkUnit`). For Protobuf messages, use `proto.Modify` (from `"golang/protobuf/v2/proto/proto"`) to apply targeted mutations. For standard Go structs, use shallow copies or localized mutation helpers. This keeps test tables readable and explicitly highlights what fields are being tested.
- **Centralize Common Test Utilities**: Never redefine generic test helpers (such as `modifyProto` or mock builders) locally across multiple test files or packages. Shared test and proto utilities must be centralized in global/shared packages (e.g., `proto.Modify` in `//internal/compat/proto`) so they are accessible globally across the entire repository.

## 6. Go Dependencies / Bazel
- Always inspect proto package `BUILD.bazel` to verify exact `go_proto_library` target name (typically ending in `_go_proto`) before adding Bazel dependencies.
