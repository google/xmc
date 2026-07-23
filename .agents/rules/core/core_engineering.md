# Core Engineering & Operational Rules (SSOT)

This document is the Single Source of Truth (SSOT) for foundational architectural, structural, and clean code guidelines across all workflows.

---

## 1. Single Responsibility Principle (SRP) & Focused Functions
- **Single Responsibility**: Every module, class, and function must have one, and only one, clear reason to change.
- **YAGNI & No Unused Parameters**: Accept no unused parameters, dead fields, or speculative arguments in function signatures or constructors.
- **20–30 Line Function Limit**: Aim to keep functions and methods within a highly readable **20–30 line target cap**. Decompose monolithic methods into pure helpers.
- **Decompose Complex Logic**: Separate business logic, state mutation, and structural data transformations from framework hooks or shell execution.

## 2. Linear Execution & Control Flow (KISS)
- **Linear Indentation**: Keep control flow strictly linear to eliminate unnecessary nesting.
- **Guard Clauses & Early Returns**: Use early `return` or `throw` guard clauses at the top of functions for boundary conditions and edge cases.
- **Eliminate Redundant `else` Blocks**: Never write `else` or `else if` blocks immediately following a control flow statement that terminates execution (`return`, `break`, `continue`, `throw`).

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
- **Default Immutability (`const` over `let`)**: Declare variables as `const` by default. Reserve `let` strictly for required local reassignment (e.g. counters).
- **Constructive Initialization**: Use spread operators (`...`), ternary expressions (`cond ? a : b`), or nullish coalescing (`??`) rather than imperative block assignments.
- **Shallow Copies Before Mutation**: Never mutate arrays or state objects directly. Always construct a shallow copy (`[...array]`, `{ ...obj }`) prior to sorting, filtering, or modifying.

## 4. Hermetic Testing, DI & Decoupling
- **Eradicate Production Mock Branches**: Never embed static mock datasets or conditional mock branches (`if (useMock)`) inside production code. Decouple mocks using Dependency Injection (DI) factory providers.
- **Accurate Structural Models**: Ensure all mock execution models match full structural requirements of production infrastructure.
- **Baseline Test Fixtures & Targeted Mutations**: Define canonical baseline fixtures for repetitive data payloads. Use mutation helpers (such as `proto.Modify` for Protobuf messages or shallow copies for standard structs) to express test cases as concise deltas.
- **Centralize Shared Utilities**: Never copy-paste helper functions or mock builders across packages. Centralize shared utilities in global packages (e.g. `proto.Modify`), eliminating duplicate code and test fixtures across unit test suites.

## 5. Strict Coverage & System Contract Alignment
- **Strict Coverage Requirements**: All feature additions and bug fixes must include unit/integration test coverage. Never reduce assertions or mask failures.
- **1-to-1 File Test Cohesion**: Every source file with business logic (e.g. `validation.go`) MUST have a corresponding unit test file (e.g. `validation_test.go`) testing exported functions directly in unit isolation.
- **Open-Source TODO Standard**: Public code `TODO` comments MUST NOT include internal Buganizer IDs (e.g. `TODO(b/12345)`). Use generic `TODO:` or public issue IDs (`TODO(#123)`).
- **Documentation Alignment**: Synchronize repository markdown documents (`README.md`, `INSTALL.md`, specs), schemas, and inline API references in the exact same pull request whenever contracts change.
