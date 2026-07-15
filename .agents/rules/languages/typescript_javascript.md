# TypeScript & Modern Angular UI Guidelines

This document defines core architectural, structural, and stylistic standards for TypeScript and Angular frontend code in the XManager UI (`cmd/ui/app/`), incorporating modern Angular architecture and clean code best practices.

---

## 1. TypeScript Strictness & Style
- **Prefer TypeScript over JavaScript**: Prefer writing TypeScript (`.ts`) over plain JavaScript (`.js`) whenever viable across frontend modules, services, and web tooling.
- **TypeScript Strictness**: Enforce strict type checking across all files. Explicitly type public method signatures, class parameters, and service return types.
- **Default Immutability (`const` over `let`)**: Declare variables as `const` by default. Reserve `let` strictly for scopes where variable reassignment is structurally required (e.g., loop index counters).
- **Constructive Initialization**: Use ternary expressions (`cond ? a : b`), object/array spread syntax (`...`), and default nullish fallbacks (`||`, `??`) to eliminate imperative variable reassignment chains.
- **Clean Import Tree**: Avoid imports utilizing more than two parent directory traversals (`../../../`). Use qualified project TypeScript path mapping aliases (e.g., `xmanager_cloud/ui/app/...`) or localized flat relative paths (`./utils`, `../common`).

## 2. State Management & Signal Immutability
- **CRITICAL - Signal Data Immutability**: Never mutate state objects or arrays returned by Angular signals or RxJS observables in-place.
- **Shallow Copy Requirement**: When sorting, filtering, or reversing array structures sourced from signals, always create a shallow copy first. Mutating array references directly corrupts reactivity state and causes subtle UI bugs.
```typescript
// ❌ INCOMPLIANT (Mutates internal signal reference directly)
const sorted = this.stateCounts().sort(comparator);

// ✔️ COMPLIANT (Creates shallow copy before sorting)
const sorted = [...this.stateCounts()].sort(comparator);
```
- **ComponentStore Updaters & Effects**: Keep Ngrx/ComponentStore callback logic linear. Decompose lengthy success or error handling callbacks inside stream operators into dedicated private handler methods (`handleUpdateSuccess`, `handleFetchError`).
- **Optional Execution**: Execute optional event callbacks using optional chaining (`options?.onSuccess?.()`).

## 3. Modern Angular Standards
- **Built-in Template Control Flow**: Exclusively use modern Angular built-in block control flow (`@if`, `@for`, `@switch`) over legacy structural directives (`*ngIf`, `*ngFor`, `*ngSwitch`).
- **Modern Signal APIs**: Standardize component inputs, outputs, models, and queries to functional signal primitives (`input()`, `output()`, `model()`, `viewChild()`). Completely eliminate legacy decorators (`@Input`, `@Output`, `@ViewChild`). When capturing queries such as `MatPaginator`, use `viewChild()` and handle assignments inside an explicit `effect()`.
- **Standalone Architecture**: Ensure components, directives, and pipes are standalone (`standalone: true`) by default. Eliminate legacy `@NgModule` declarations to keep dependencies flat and tree-shakable.

## 4. Component Clean Code & Single Responsibility
- **Focused View Controllers**: Component classes must remain strictly focused on view coordination and localized user interactions. Move complex data transformations, filtering calculations, and structural grouping into dedicated utility files, localized pure helper functions, or services.
- **Declarative Style Maps**: Avoid deep `if/else` or `switch` conditional chains in view logic or templates. Extract styling class decisions, severity markers, and status colors into declarative dictionary `Map`s:
```typescript
// ✔️ COMPLIANT (Declarative styling mapping)
private static readonly STATUS_CHIP_MAP = new Map<WorkUnitState, ChipStyle>([
  [WorkUnitState.RUNNING, { class: 'bg-blue', icon: 'sync' }],
  [WorkUnitState.FAILED, { class: 'bg-red', icon: 'error' }],
]);
```
- **Standardized Batch Operations**: Centralize repetitive multi-step asynchronous UI workflows (confirmation dialogs, spinners, snackbars) into standardized reusable helper methods (e.g., `executeBatchOperation`).

## 5. Dependency Injection (DI) & Hermetic Mocking
- **Eradicate Embedded Mock Data**: Never import static mock datasets (e.g., `mock_data.ts`) or include conditional mock execution branches (`if (this.useMockData)`) inside production Angular service classes. Production services must contain 100% pure real execution logic.
- **Standalone Mock Classes**: Move all mock implementations and stubbed responses into dedicated standalone mock service classes (e.g., `MockExperimentService extends ExperimentService`) located in dedicated testing directories (e.g., `service/testing/`).
- **Application DI Polymorphism**: Register DI factory providers in the core application configuration (`app_config.ts`) to dynamically resolve the correct service implementation based on runtime environmental tokens:
```typescript
{
  provide: ExperimentService,
  useFactory: () => inject(USE_MOCK_DATA) ? new MockExperimentService() : new ExperimentService(),
}
```

## 6. RxJS & Stream Pipelines
- **Lifecycle Initialization Helpers**: Extract complex multi-pipe observable constructions out of constructors and `ngOnInit` hooks into clear initialization methods (e.g., `initExperimentStream()`, `initUrlsStream()`).
- Cleanly import RxJS operators directly from `'rxjs'` to optimize tree-shaking and bundle size.
