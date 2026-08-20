# Core Engineering & Operational Rules

This document outlines foundational architectural, structural, and clean code guidelines for all engineering workflows across the codebase.

---

## 1. Single Responsibility Principle (SRP), YAGNI & Focused Functions
- **Single Responsibility**: Every module, class, and function must have one, and only one, clear reason to change.
- **YAGNI & No Unused Parameters**: Adhere strictly to YAGNI (*You Aren't Gonna Need It*). Do NOT accept unused parameters, dead fields, or speculative arguments in function signatures or class constructors. Only declare parameters that are actively read, validated, or processed within the function body (excluding mandatory protocol/runtime interface overrides such as context manager `__exit__`).
- **Function Limit Target**: Aim to keep functions and methods within a highly readable **20–30 line target cap**. Decompose monolithic methods into localized, pure helper functions.
- **Decompose Complex Logic**: Separate business logic, state mutation, and structural data transformations from framework hooks, lifecycle orchestrations, or shell command invocations.
- **Prohibit Generic Dumping-Ground Names & Suffixes/Prefixes (`util`, `utils`, `helper`, `helpers`, `utility`, `utilities`, `misc`, `common`, `tools`, `shared`)**:
  - **No Generic Names, Folders, or Suffixes**: Never name files, methods, functions, classes, interfaces, packages, or parent directories using generic terms or compound suffixes/prefixes (e.g., `util`, `utils`, `urlutil`, `timeutil`, `emailutil`, `helper`, `helpers`, `stringhelper`, `utility`, `utilities`, `misc`, `common`, `tools`, `shared`). Catch-all names and generic suffixes obscure domain intent and turn modules into dumping grounds for unrelated code.
  - **SRP Violation Warning**: Reaching for generic terms or suffixes like `util`, `utils`, `helper`, or `timeutil` is a strong signal of a Single Responsibility Principle (SRP) violation—it indicates that the code lacks a clear, cohesive domain scope. Never group unrelated capabilities under a generic directory (e.g. `pkg/util/`) or apply generic suffixes (`urlutil`).
  - **Explicit Domain Naming & Decomposition**: Always use direct domain-oriented names that describe the underlying concept or action without generic filler words. Separate distinct capabilities into clean, dedicated domain packages/modules (e.g., `pkg/url/parser.go`, `pkg/time/formatter.go`, `pkg/email/validator.go` or `url_parser.go`, `date_formatter.py`, `email_validator.go`).

## 2. Linear Execution & Control Flow (KISS)
- **Linear Indentation**: Keep control flow strictly linear to eliminate unnecessary nesting and simplify reasoning.
- **Guard Clauses & Early Returns**: Use early `return` or `throw` guard clauses at the top of functions to handle boundary conditions, invalid inputs, and edge cases.
- **Eliminate Redundant `else` Blocks**: Never write `else` or `else if` blocks immediately following a control flow statement that terminates execution (`return`, `break`, `continue`, or `throw`).

```typescript
// ❌ INCOMPLIANT (Deep nesting and redundant else blocks)
function processUser(user: User | null) {
  if (user !== null) {
    if (user.isActive) {
      return executeTask(user);
    } else {
      throw new Error("User inactive");
    }
  } else {
    throw new Error("User missing");
  }
}

// ✔️ COMPLIANT (Linear execution with guard clauses)
function processUser(user: User | null) {
  if (!user) {
    throw new Error("User missing");
  }
  if (!user.isActive) {
    throw new Error("User inactive");
  }
  return executeTask(user);
}
```

## 3. Default Immutability & Safe Data Handling
- **Default Immutability (`const` over `let`)**: Declare variables as `const` by default. Reserve `let` strictly for instances where localized value reassignment is an explicit, unavoidable structural requirement (e.g., loop counters, accumulators).
- **Domain Model Cleanliness over Transport Serialization**: Frontend view models and ambient TypeScript interfaces (*.d.ts) must remain pure, strongly typed domain representations. Do NOT leak wire transport quirks (such as Proto3 int64 string JSON serialization) into UI interface fields as number | string union types. Handle wire normalization at service boundaries (e.g., converting serialized string representations of 64-bit integers to bigint or number).
- **Constructive Initialization**: Use spread operators (`...`), ternary expressions (`cond ? a : b`), or nullish coalescing (`??`) to construct values rather than imperatively assigning `let` variables across multiple blocks.
- **Shallow Copies Before Mutation**: Never mutate arrays or state objects directly (especially data returned by signals, observables, or shared states). Always construct a shallow copy (`[...array]`, `{ ...obj }`) prior to sorting, filtering, or modifying properties.

## 4. High-Fidelity Test Doubles & Decoupling
- **Accurate Structural Models**: Ensure all mock execution models and stubbed interfaces match the full structural requirements of underlying production infrastructure.
- **Parametric Order Matching**: For HTTP or service query mocks (e.g., `HttpParams` or query parameter verification), ensure parameter maps serialize and compare in the exact order expected by matching criteria.
- **No Hallucinated Identifiers**: Use valid, consistent mock identifiers in unit test suites (matching base fixture schemas such as `experiments/1` rather than arbitrary unlinked IDs like `123`).
- **Eradicate Production Mock Branches**: Never embed static mock datasets or conditional mock execution branches (`if (useMock)`) inside production application code or service layers. Decouple mocks using Dependency Injection (DI) factory providers.
- **Centralize Reusable Domain Packages & Test Fixtures**: Never copy-paste or redefine identical functions, mock mutators, or test fixtures locally across multiple packages. Centralize reusable logic in domain-specific packages (e.g., `internal/compat/proto` or dedicated test libraries) rather than generic `utils` packages to maintain DRY across the repository.
- **Baseline Test Fixtures & Targeted Mutations**: In unit tests with repetitive data payloads, avoid copy-pasting monolithic structures. Define a canonical baseline fixture and use mutation helpers (such as `proto.Modify` for Protobuf messages or shallow copies/local helpers for standard structs) to express each test case as a concise delta from the base.
- **No Silent Null-Bypassing in Test Helpers**: Test helper functions and constructor fixtures (such as `newTestHandler()`) MUST NOT silently pass `nil` or `null` for newly introduced dependencies unless a test case explicitly tests `nil`/`null`-safety. When new capabilities or metrics are added to a component, test helpers should provide fully initialized instances or configurable fixtures so that test cases validate the side effects (metrics, events, logs) of the new feature.

## 5. Strict Testing Coverage & PR Granularity
- **Strict Coverage Requirements**: All feature additions, bug fixes, and code modifications must include comprehensive unit and integration test coverage. Never reduce assertions or mask test failures.
- **1-to-1 File Test Cohesion**: Every source file containing business, parsing, or data transformation logic (e.g. `validation.go`) MUST have a corresponding dedicated unit test file (e.g. `validation_test.go`) testing its exported functions directly in unit isolation. Relying solely on higher-level integration or HTTP handler tests (`handler_test.go`) to transitively cover helper logic is not permitted.
- **UI Visual Screenshot Testing**: UI component visual screenshot tests must use direct custom component host tags (`<xmc-*>`), rely on shared test utilities (`test_utils.ts`) for standard viewport sizing and layout formatting, configure `maxDiffPixelRatio` for cross-platform CI font rendering, and synchronize baseline PNG snapshots (see [TypeScript & Modern Angular Guidelines](languages/typescript_javascript.md#8-playwright-visual-screenshot-testing)).
- **Open-Source Code TODO Standard**: Inline code `TODO` comments in public/OSS source files (under `sdks/` or open packages) MUST NOT include internal Buganizer IDs (e.g. `TODO(b/12345)`). Use generic `TODO:` tags or public GitHub issue numbers (`TODO(#123)`).
- **Small, Focused PRs**: Keep Pull Requests small, localized, and logically split. Each PR should address a single cohesive task or change step to enable fast, high-quality reviews.
- **System Contract & API Changes**:
  - **Mandatory User Confirmation**: Seek explicit user confirmation before introducing breaking changes to public APIs, protobuf schemas, CLI flags, or core system contracts.
  - **Documentation Alignment**: Synchronize repository markdown documents (including, but not limited to, `README.md`, `INSTALL.md`, and component specs), schemas, and inline API references within the exact same pull request whenever system contracts or behaviors change.

## 6. Post-Implementation Reflection & Meta-Learning
- **Iterative Reflection Triggers**: Trigger the [Iterative Reflection Skill](../skills/xmc_iterative_reflection/SKILL.md) after major implementations, following PR review comment resolutions (to diagnose and codify missing guidelines/tools), or upon explicit user request. Validate candidate rule additions empirically using the [XMC Trajectory Comparison Skill](../skills/xmc_trajectory_comparison/SKILL.md). Do NOT trigger for routine minor typo fixes where no underlying guideline was violated.


## 7. System Attribute Immutability & Precedence
- **System Key Precedence**: User-supplied custom maps (labels, annotations, environment variables) MUST NEVER overwrite reserved internal system attributes or security identifiers (`xid`, `wid`, `eid`, `queue-name`):
  ```go
  if systemMap == nil {
      systemMap = make(map[string]string)
  }
  for k, v := range userMap {
      if _, exists := systemMap[k]; !exists {
          systemMap[k] = v
      }
  }
  ```

## 8. Documentation Preservation & Production Manifest Isolation
- **100% Comment Preservation During Refactoring**: When decomposing monolithic source files into subpackages, preserve ALL existing docstrings, package headers, section dividers (`// ------`), and step-by-step numbered algorithm comments (`// 1. ...`) verbatim inside method bodies.
- **Production Manifest Isolation & Sandbox Immutability**: NEVER modify production deployment manifests (`deploy/`, `helm/`, production SQL DDLs) in-place with `sed -i` or in-place file rewrites to work around test container or local cluster limitations. When environment-specific manifest modifications are required for local test clusters (such as KinD image overrides or test namespaces), test runner scripts MUST copy manifests to a uniquely generated ephemeral temporary directory (using `mktemp -d` to avoid multi-user or concurrent CI collisions) or pass dynamic Helm value overrides (`--set`) to maintain clean, untouched git working trees.
- **Pinned Container Image Versions in Manifests**: All container images in declarative Kubernetes manifests (`deploy/`) MUST pin specific stable versions or semantic release tags (e.g., `1.36.1`, `v2.4.3`) and NEVER use mutable `latest` tags to ensure deterministic deployments and cache reproducibility.
- **Directory Separation of Concerns (SRP) for Tests & Tools**: All end-to-end (E2E), integration, and regression test suites must reside under `test/<type>/` (e.g., `test/e2e/`, `test/regression/`). The `tools/` directory is reserved strictly for standalone developer CLI utilities, automation scripts, and build tooling.
- **Library First Policy**: Inspect third-party library documentation for built-in capabilities (e.g. `clickhouse.WithInitScripts()`) prior to implementing custom parsers or state machines.

## 9. Bazel CI Diagnostic Protocol & Zero Task Polling
- **No Speculative Attribute Changes**: NEVER modify Bazel test parameters (`size`, `timeout`, `tags`) speculatively upon CI test failure. Always inspect complete test logs via `bazel test --test_output=errors //...` to identify exact failure root causes.
- **Reactive Background Task Handling**: NEVER poll background tasks in a loop using `manage_task action=status`. Rely on system reactive notifications after launching background tasks.
