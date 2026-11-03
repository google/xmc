# Core Engineering & Operational Rules

This document outlines foundational architectural, structural, and clean code guidelines for all engineering workflows across the codebase.

---

## 1. Single Responsibility Principle (SRP), YAGNI & Focused Functions
- **Single Responsibility**: Every module, class, and function must have one, and only one, clear reason to change.
- **YAGNI & No Unused Parameters**: Adhere strictly to YAGNI (*You Aren't Gonna Need It*). Do NOT accept unused parameters, dead fields, or speculative arguments in function signatures or class constructors. Only declare parameters that are actively read, validated, or processed within the function body (excluding mandatory protocol/runtime interface overrides such as context manager `__exit__`). YAGNI applies to scope too: if considering work the request did not ask for (speculative defensive guards, speculative flags, migration engines, refactors of code being moved), propose it and get user confirmation before implementing it. Tests and docs that the requested change requires are in scope.
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
- **Unbounded Collection Querying & Anti-Truncation**: Frontend components, services, and API consumers MUST NOT fetch a single arbitrary page (e.g., requesting `pageSize: 100`) and filter client-side when querying unbounded collections. Unbounded collections MUST use server-side filtering (`filter`), user pagination controls (`pageToken`), or direct resource lookups (`Get*` by ID/name) per [Storage Layer Guidelines](../../internal/storage/AGENTS.md#4-deterministic-default-ordering--pagination-for-unbounded-collections).

## 4. High-Fidelity Test Doubles & Decoupling
- **Accurate Structural Models**: Ensure all mock execution models and stubbed interfaces match the full structural requirements of underlying production infrastructure.
- **Parametric Order Matching**: For HTTP or service query mocks (e.g., `HttpParams` or query parameter verification), ensure parameter maps serialize and compare in the exact order expected by matching criteria.
- **No Hallucinated Identifiers**: Use valid, consistent mock identifiers in unit test suites (matching base fixture schemas such as `experiments/1` rather than arbitrary unlinked IDs like `123`).
- **Eradicate Production Mock Branches & Static Fixtures**: Never embed static mock datasets, hardcoded test fixtures, default sample models, or conditional mock execution branches (`if (useMock)` or fallback mock generators) inside production application code, components, or service layers. Production components and services must strictly render live data or transition to standard empty/not-found states upon failure. Decouple mocks using Dependency Injection (DI) factory providers.
- **Strict Mock & Fixture Isolation (Mocks Belong Exclusively in Test Files)**: All mock datasets, sample entities, and test doubles belong strictly in dedicated test files (`*_test.ts`, `*_test.go`, `test_*.py`) or dedicated testing libraries (e.g., `cmd/ui/app/testing/mock_*.ts`, `test/fixtures/`). Production source files must never define or export sample test data.
- **Centralize Reusable Domain Packages & Test Fixtures**: Never copy-paste or redefine identical functions, mock mutators, or test fixtures locally across multiple packages. Centralize reusable logic in domain-specific packages (e.g., `internal/prototest` or dedicated test libraries) rather than generic `utils` packages to maintain DRY across the repository.
- **Baseline Test Fixtures & Targeted Mutations**: In unit tests with repetitive data payloads, avoid copy-pasting monolithic structures. Define a canonical baseline fixture and use mutation helpers (such as `prototest.Modify` for Protobuf messages or shallow copies/local helpers for standard structs) to express each test case as a concise delta from the base.
- **No Silent Null-Bypassing in Test Helpers**: Test helper functions and constructor fixtures (such as `newTestHandler()`) MUST NOT silently pass `nil` or `null` for newly introduced dependencies unless a test case explicitly tests `nil`/`null`-safety. When new capabilities or metrics are added to a component, test helpers should provide fully initialized instances or configurable fixtures so that test cases validate the side effects (metrics, events, logs) of the new feature.

## 5. Strict Testing Coverage & PR Granularity
- **Strict Coverage Requirements**: All feature additions, bug fixes, and code modifications must include comprehensive unit and integration test coverage. Never reduce assertions or mask test failures.
- **1-to-1 File Test Cohesion**: Every source file containing business, parsing, or data transformation logic (e.g. `validation.go`) MUST have a corresponding dedicated unit test file (e.g. `validation_test.go`) testing its exported functions directly in unit isolation. Relying solely on higher-level integration or HTTP handler tests (`handler_test.go`) to transitively cover helper logic is not permitted.
  - **Standalone Example Scripts & Nightly E2E Verification Gate (`examples/`)**: Standalone example entrypoint, launch, and container workload scripts under `examples/` (e.g. `examples/*/launch.py`, `examples/xdash_dashboard/create_dashboard.py`, `examples/*/worker.py`) do not require 1-to-1 unit test files; instead, every runnable `examples/<name>/` directory MUST include a corresponding Nightly E2E verification suite (`tools/nightly_tests/examples_verification/<name>_verify.py`, a `py_binary(name = "<name>_verify")` target in `tools/nightly_tests/examples_verification/BUILD.bazel`, and a matrix entry in `.github/workflows/nightly-examples-e2e.yml`) as documented in [`tools/nightly_tests/README.md`](../../tools/nightly_tests/README.md) and enforced by `tools/nightly_tests/check_examples_coverage.py` per [Repository-Scanning Gate Invariants](skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints) (or explicitly opt out with an explanatory comment via `EXEMPT_EXAMPLES` in `tools/nightly_tests/check_examples_coverage.py`). Shared helper or algorithmic modules within examples (e.g., `training_metrics.py`, `init_step.py`) still require dedicated unit test files.
- **Shared Dirty-Environment E2E & Pagination Testing**: E2E tests and UI verifiers MUST scope lookups by target XID/entity ID or server-side filters rather than assuming clean state or unfiltered first-page presence in shared clusters (`xmc-nightly`; see [`tools/nightly_tests/README.md`](../../tools/nightly_tests/README.md#step-1-implement-the-verifier-in-examples_verification)), and `List*` integration tests MUST verify default ordering and multi-page traversal when `N > page_size` per [Storage Layer Guidelines](../../internal/storage/AGENTS.md#4-deterministic-default-ordering--pagination-for-unbounded-collections).
- **UI Visual Screenshot Testing**: UI visual screenshot tests MUST only be created for full top-level page views (e.g. `/resources`, `/admin`), relying on shared test utilities (`test_utils.ts`) for standard viewport sizing, configuring `maxDiffPixelRatio` for cross-platform CI font rendering, explicitly waiting for all synchronous and asynchronous child elements to load, and synchronizing baseline PNG snapshots (see [TypeScript & Modern Angular Guidelines](languages/typescript_javascript.md#8-playwright-visual-screenshot-testing)).
- **Open-Source Code TODO Standard**: Inline code `TODO` comments in public/OSS source files (under `sdks/` or open packages) MUST NOT include internal Buganizer IDs (e.g. `TODO(b/12345)`). Use generic `TODO:` tags or public GitHub issue numbers (`TODO(#123)`).
- **Small, Focused PRs & Sizing Thresholds**: Keep Pull Requests small, localized, and logically split. Each PR should address a single cohesive task or change step to enable fast, high-quality reviews.
  - **Sizing Thresholds (Evaluated via `tools/analyze_diff_size.py`)**:
    - **Production Code**: $\ge 400$ lines triggers mandatory split verification.
    - **Combined Code (Prod + Tests)**: $\ge 1,000$ lines triggers mandatory split verification.
    - **Documentation / Markdown**: $\ge 1,000$ lines triggers modularization check.
    - *Generated Code & Lockfiles*: Automatically excluded from sizing.
  - **Vertical Slicing over Horizontal Splitting**: Always prioritize vertical slices (end-to-end usable flows spanning models, logic, and tests) over horizontal architectural layers (e.g. database-only PRs).
  - **Dead Code & Reachability Invariant**: Intermediate PRs in a stack must NOT submit dead, uncalled, or orphaned logic (even with unit tests). Every PR merged to `main` must have an active production call site or be a self-contained runnable entrypoint (e.g., a new CLI binary).
  - **Mandatory Subagent Audit**: If any sizing threshold is exceeded, agents MUST spawn the PR Split Verification Subagent per [XMC PR Split Verification Skill](../skills/xmc_pr_split_verification/SKILL.md) and propose a stacked PR plan via [`gh-stack`](conversation://skills/gh-stack) before opening a PR.
- **System Contract & API Changes**:
  - **Mandatory User Confirmation**: Seek explicit user confirmation before introducing breaking changes to public APIs, protobuf schemas, CLI flags, or core system contracts.
  - **Documentation Alignment**: Synchronize repository markdown documents (including, but not limited to, `README.md`, `INSTALL.md`, and component specs), schemas, and inline API references within the exact same pull request whenever system contracts or behaviors change.

## 6. Post-Implementation Reflection & Meta-Learning
- **Iterative Reflection Triggers**: Trigger the [Iterative Reflection Skill](../skills/xmc_iterative_reflection/SKILL.md) after major implementations, following PR review comment resolutions (to diagnose and codify missing guidelines/tools), or upon explicit user request. Validate candidate rule additions empirically using the [XMC Trajectory Comparison Skill](../skills/xmc_trajectory_comparison/SKILL.md). Do NOT trigger for routine minor typo fixes where no underlying guideline was violated.
- **Route Findings Before Drafting Fixes**: Every reflection finding MUST be assigned a destination via the control ladder in [Rule Authoring Standards](rule_authoring_standards.md). Prose rules are a last-resort control: if an invariant is mechanically checkable it MUST become a lint rule instead.


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
- **Pinned Container Image Versions in Production Manifests**: All container images in production declarative Kubernetes manifests or production Helm values (under deploy/) MUST pin specific stable versions or semantic release tags (such as 1.36.1 or v2.4.3) and NEVER use mutable latest tags to ensure deterministic deployments and cache reproducibility.
- **Directory Separation of Concerns (SRP) for Tests & Tools**: All end-to-end (E2E), integration, and regression test suites must reside under `test/<type>/` (e.g., `test/e2e/`, `test/regression/`). The `tools/` directory is reserved strictly for standalone developer CLI utilities, automation scripts, and build tooling.
- **Library First Policy**: Inspect third-party library documentation for built-in capabilities (e.g. `clickhouse.WithInitScripts()`) prior to implementing custom parsers or state machines.

## 9. Diagnostic Protocol (Bazel CI and Agent Shell) and Zero Task Polling
- **No Speculative Attribute Changes**: NEVER modify Bazel test parameters (`size`, `timeout`, `tags`) speculatively upon CI test failure. Always inspect complete test logs via `bazel test --test_output=errors //...` to identify exact failure root causes.
- **PATH-First Diagnosis for Missing CLIs**: Agent shells are non-login and do not load the user's shell profile, so treat `command not found` or "required but not installed" as a PATH problem first: search common install locations (e.g. `~/.terraform/bin`, `~/go/bin`, `~/.local/bin`, `./cluster-toolkit`) and add the containing directory to `PATH` before declaring the dependency missing. NEVER propose installing a CLI before completing that search.
- **Reactive Background Task Handling**: Run long agent commands through the agent's native asynchronous command execution with completion notification, and keep their logs under `${ARTIFACT_DIR:-/tmp}` per the [Resumption State Loading idiom](skills_and_automation.md#9-workflow-orchestration--remote-automation-invariants). NEVER detach them with `nohup`, `setsid`, or a trailing `&`: the detached process escapes tracking and its log can be lost. NEVER poll a background task's status in a loop; rely on the completion notification.
