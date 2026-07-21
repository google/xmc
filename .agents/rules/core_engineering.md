# Core Engineering & Operational Rules

This document outlines foundational architectural, structural, and clean code guidelines for all engineering workflows across the codebase.

---

## 1. Single Responsibility Principle (SRP) & Focused Functions
- **Single Responsibility**: Every module, class, and function must have one, and only one, clear reason to change.
- **Function Limit Target**: Aim to keep functions and methods within a highly readable **20–30 line target cap**. Decompose monolithic methods into localized, pure helper functions.
- **Decompose Complex Logic**: Separate business logic, state mutation, and structural data transformations from framework hooks, lifecycle orchestrations, or shell command invocations.

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
- **Constructive Initialization**: Use spread operators (`...`), ternary expressions (`cond ? a : b`), or nullish coalescing (`??`) to construct values rather than imperatively assigning `let` variables across multiple blocks.
- **Shallow Copies Before Mutation**: Never mutate arrays or state objects directly (especially data returned by signals, observables, or shared states). Always construct a shallow copy (`[...array]`, `{ ...obj }`) prior to sorting, filtering, or modifying properties.

## 4. High-Fidelity Test Doubles & Decoupling
- **Accurate Structural Models**: Ensure all mock execution models and stubbed interfaces match the full structural requirements of underlying production infrastructure.
- **Parametric Order Matching**: For HTTP or service query mocks (e.g., `HttpParams` or query parameter verification), ensure parameter maps serialize and compare in the exact order expected by matching criteria.
- **No Hallucinated Identifiers**: Use valid, consistent mock identifiers in unit test suites (matching base fixture schemas such as `experiments/1` rather than arbitrary unlinked IDs like `123`).
- **Eradicate Production Mock Branches**: Never embed static mock datasets or conditional mock execution branches (`if (useMock)`) inside production application code or service layers. Decouple mocks using Dependency Injection (DI) factory providers.

## 5. Strict Testing Coverage & PR Granularity
- **Strict Coverage Requirements**: All feature additions, bug fixes, and code modifications must include comprehensive unit and integration test coverage. Never reduce assertions or mask test failures.
- **Small, Focused PRs**: Keep Pull Requests small, localized, and logically split. Each PR should address a single cohesive task or change step to enable fast, high-quality reviews.
- **System Contract & API Changes**:
  - **Mandatory User Confirmation**: Seek explicit user confirmation before introducing breaking changes to public APIs, protobuf schemas, CLI flags, or core system contracts.
  - **Documentation Alignment**: Synchronize repository markdown documents (including, but not limited to, `README.md`, `INSTALL.md`, and component specs), schemas, and inline API references within the exact same pull request whenever system contracts or behaviors change.

## 6. Post-Implementation Reflection & Meta-Learning
- **Selective Triggers**: Trigger the `iterative_reflection` skill ([SKILL.md](file://.agents/skills/iterative_reflection/SKILL.md)) only after major implementations, PRs with high comment volume (>3-5 comments), or explicit user request. Do NOT trigger for routine minor edits.
