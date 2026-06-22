---
name: UI Best Practices
description: Comprehensive best practice guidelines and refactoring standards for the XManager Angular UI codebase, derived from Phase 1 and Phase 2 refactoring patterns.
---

# XManager Angular UI Refactoring & Best Practices Guide

This document defines the core architectural, structural, and stylistic standards for all TypeScript and Angular code within the XManager UI component (`cmd/ui/app/`). All future refactoring and feature development must adhere strictly to these principles.

---

## 1. State Management & Signals

### Immutability of Signal Data
- **CRITICAL**: Never mutate the state returned by an Angular signal or RxJS observable in-place.
- When sorting, filtering, or reversing arrays sourced from signals, always create a shallow copy first.
- **Why**: Signals return direct references to their internally managed data values. Mutating an array directly (e.g., `this.mySignal().sort()`) bypasses the reactivity engine, corrupts the underlying state, and causes subtle, difficult-to-debug UI inconsistencies or Ngrx violations.
```typescript
// ❌ INCOMPLIANT (Mutates internal signal reference)
const sorted = this.stateCounts().sort(comparator);

// ✔️ COMPLIANT (Creates shallow copy before sorting)
const sorted = [...this.stateCounts()].sort(comparator);
```

### ComponentStore Updaters & Effects
- **Linear Callback Logic**: Keep Ngrx/ComponentStore effects cleanly structured. Extract lengthy success or error handling callbacks into dedicated private methods (e.g., `handleUpdateSuccess`, `handleFetchError`) rather than nesting massive anonymous blocks inside the stream operators.
- **Optional Execution**: Execute optional event hooks (`onSuccess`, `onFinish`) gracefully using optional chaining (`?.()`).
```typescript
// ✔️ COMPLIANT
this.updateStore(updatedState);
options?.onSuccess?.();
```

---

## 2. Component Clean Code & SRP

### Decomposing Massive Logic (SRP)
- Keep all component classes strictly focused on view coordination and localized UI interaction.
- Extract complex data transformation, filtering, and structural grouping out of component lifecycle hooks and actions. Move them into dedicated utility files, localized pure helper functions, or specialized Angular services.

### Conditional Formatting & UI Classes
- Avoid deep `if/else` or `switch` conditional chains in view logic or templates.
- Extract styling class decisions, severity markers, and status colors into declarative dictionary `Map`s or clean pure helper methods.
```typescript
// ✔️ COMPLIANT (Declarative styling mapping)
private static readonly STATUS_CHIP_MAP = new Map<WorkUnitState, ChipStyle>([
  [WorkUnitState.RUNNING, { class: 'bg-blue', icon: 'sync' }],
  [WorkUnitState.FAILED, { class: 'bg-red', icon: 'error' }],
]);
```

### Batch Operations & Standardized Feedback
- Standardize long-running UI operations (e.g., stopping or restarting work units and experiments).
- Centralize confirmation dialog rendering, asynchronous service execution, visual loading indicators (spinners/bars), and user feedback (snackbars) into standardized reusable helper methods (e.g., `executeBatchOperation`).

---

## 3. RxJS & Streams

### Lifecycle Stream Initialization
- Avoid crowding constructors or `ngOnInit` hooks with huge RxJS multi-pipe declaration chains.
- Break observable pipeline constructions into specific, well-named initialization helper methods (e.g., `initExperimentStream()`, `initUrlsStream()`).

### Stream Operators
- Keep import lists clean by importing RxJS operators directly from `'rxjs'` when supported by your modular setup, or use flattened operator structures.

---

## 4. TypeScript Style & Architecture

### Favor `const` over `let`
- Always use `const` for variable declarations by default.
- Reserve `let` only for scopes where variable reassignment is an absolute, unavoidable requirement (e.g., localized loop counters or specific accumulators).
- Utilize ternary fallback assignments, the object spread operator (`...`), and default nullish parameters (`||`, `??`) to eliminate unneeded `let` reassignments.

### Standardized Imports
- **Avoid deep relative paths**: Refactor any imports utilizing more than two parent directory traversals (`../../../`).
- Use the fully qualified project TypeScript path mapping aliases (e.g., `xmanager_cloud/ui/app/...`) or localized flat relative paths (`./utils`, `../common`) to establish a clear, predictable dependency graph.
- Keep `@Component` decorator imports beautifully organized, multi-lined, and sorted with internal tooling comments (e.g., `// go/keep-sorted`).

---

## 5. Angular 22 Modernization Standards (Phase 2)

### Built-in Template Control Flow
- Always favor modern Angular built-in control flow (`@if`, `@for`, `@switch`) over legacy directive alternatives (`*ngIf`, `*ngFor`, `*ngSwitch`).
- **Why**: Built-in control flow works cleanly at the compiler level, significantly reduces bundle size, improves template type checking, and executes with significantly lower runtime overhead.

### Modern Signals APIs
- Standardize all component inputs, outputs, model bindings, and queries to use modern functional signal APIs (`input()`, `output()`, `model()`, `viewChild()`).
- Completely eliminate legacy decorator counterparts (`@Input`, `@Output`, `@ViewChild`).
- When replacing a `@ViewChild` setter (previously used to register dependencies like `MatPaginator`), use `viewChild()` and handle assignment inside an explicit `effect()`.

### Standalone Architectures
- Ensure all components, directives, and pipes are standalone (`standalone: true`) by default.
- Eradicate legacy `@NgModule` orchestrations to keep dependencies flat, predictable, and tree-shakable.

---

## 6. Dependency Injection (DI) & Hermetic Mocking

### Eradicate Embedded Mock Data Antipattern
- **Pure Production Services**: Never import mock data dictionaries (e.g., `mock_data.ts`) or embed inline mock execution branches (`if (this.useMockData) return this.mock(...)`) inside production Angular service classes. Production services must contain 100% pure real HTTP business logic.
- **Standalone Mock Classes**: Extract all mock data generation logic and stubbed responses into dedicated standalone mock service classes (e.g., `MockExperimentService extends ExperimentService`) located in dedicated testing folders (e.g., `service/testing/`).

### Application DI Polymorphism
- Register DI factory providers in the primary application configuration (`app_config.ts`) to dynamically resolve the correct service implementation based on runtime environmental tokens:
```typescript
{
  provide: ExperimentService,
  useFactory: () => inject(USE_MOCK_DATA) ? new MockExperimentService() : new ExperimentService(),
}
```
- **Why**: This completely decouples mock payloads from production bundles, dramatically reduces production application bundle sizes, enforces clean Separation of Concerns (SRP), and preserves strict compile-time TypeScript type safety.
- **Independent Testing**: Always construct independent unit testing suites to separately verify both the real HTTP execution contract (using `HttpTestingController`) and the standalone mock service behavior.
