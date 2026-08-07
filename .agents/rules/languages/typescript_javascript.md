# TypeScript & Modern Angular UI Guidelines

This document defines core architectural, structural, and stylistic standards for TypeScript and Angular frontend code in the XManager UI (`cmd/ui/app/`), incorporating modern Angular architecture and clean code best practices.

---

## 1. TypeScript Strictness & Style
- **Prefer TypeScript over JavaScript**: Prefer writing TypeScript (`.ts`) over plain JavaScript (`.js`) whenever viable across frontend modules, services, and web tooling.
- **TypeScript Strictness**: Enforce strict type checking across all files. Explicitly type public method signatures, class parameters, and service return types.
- **Default Immutability (`const` over `let`)**: Follow default immutability standards defined in [Core Engineering Standards](../core_engineering.md#3-default-immutability--safe-data-handling). Declare variables as `const` by default, reserving `let` strictly for scopes where reassignment is structurally required.
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
- **Template Access Encapsulation**: Use the `protected` visibility modifier on class members, methods, and signals accessed exclusively by component HTML templates (e.g. `protected readonly state = signal(...)`), maintaining minimal public component APIs.
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
- **Eradicate Embedded Mock Data**: Follow DI mock decoupling standards defined in [Core Engineering Standards](../core_engineering.md#4-high-fidelity-test-doubles--decoupling). Never import static mock datasets or embed conditional mock execution branches inside production Angular service classes.
- **Standalone Mock Classes**: Move all mock implementations and stubbed responses into dedicated standalone mock service classes (e.g., `MockExperimentService extends ExperimentService`) located in dedicated testing directories (e.g., `service/testing/`).
- **Application DI Polymorphism**: Register DI factory providers in the core application configuration (`app_config.ts`) to dynamically resolve the correct service implementation based on runtime environmental tokens:
```typescript
{
  provide: ExperimentService,
  useFactory: () => inject(USE_MOCK_DATA) ? new MockExperimentService() : new ExperimentService(),
}
```

## 6. RxJS & Stream Pipelines
- **RxJS Interop Signal Conversion (`toSignal`)**: Prefer converting RxJS Observables to Signals using `toSignal()` when consuming asynchronous data streams within Angular components and templates.
- **Lifecycle Initialization Helpers**: Extract complex multi-pipe observable constructions out of constructors and `ngOnInit` hooks into clear initialization methods (e.g., `initExperimentStream()`, `initUrlsStream()`).
- Cleanly import RxJS operators directly from `'rxjs'` to optimize tree-shaking and bundle size.

## 7. Angular Component Testing & DOM Interaction Standards
- **Mandatory DOM Interaction Coverage**: Every Angular component test suite MUST test all interactive template workflows:
  - Dynamic list additions/deletions (e.g. clicking add/remove buttons and asserting resulting DOM element counts).
  - Form control value updates (`input`, `change` events) and boundary validation (invalid/negative inputs).
  - Output signal emissions following user click actions.
- **Prefer Component Harnesses & Top-Level DOM Selector Constants**: Prioritize using Angular CDK `ComponentHarness` classes for DOM interactions and assertions to insulate tests from template changes. When harnesses are not available or practical, never repeat raw CSS query strings (`.my-btn`, `#my-input`) inside test bodies; extract all DOM selectors to named constants at the top of the test file:
  ```typescript
  const AMOUNT_INPUT_SELECTOR = '#amount-input';
  const ADD_BTN_SELECTOR = '.add-resource-btn';
  ```

## 8. Playwright Visual Screenshot Testing
- **Mandatory Screenshot Tests for Angular Components**: Whenever adding or modifying Angular components in `cmd/ui/app/resource_dashboard/` (or UI components across `cmd/ui/app/`), every Angular component MUST have a corresponding Playwright visual screenshot test spec located under `cmd/ui/screenshot_tests/` (e.g. `cmd/ui/screenshot_tests/resource_dashboard/<component_name>.screenshot.spec.ts`).
- **Direct Custom Component Host Tags (`<xmc-*>`)**: Instead of wrapping test components in generic container tags like `<div class="...">`, use direct custom component element tags (e.g. `<xmc-smart-resource-finder>`, `<xmc-rod-queue-card>`, `<xmc-rod-subnav-tabs>`) for component harness wrappers so it is explicitly clear which component is being tested.
- **Shared Screenshot Test Harness & Standard Viewport**: Component visual screenshot tests MUST rely on the shared test utilities (`setupScreenshotEnvironment` and `mountComponentHarness` in `test_utils.ts`) which configure a standard 16:9 aspect ratio viewport (`1600x900`) and universal route-agnostic container mounting without feature-specific layout dependencies (such as `.rod-container`). Do not add rules or manual steps for agents to enforce or check pixel dimensions with external CLI tools.
- **Realistic Layout Dimensions & Minimal Styling**: Component harness wrappers MUST define ONLY essential spatial bounding box dimensions and layout rules (e.g., `width`, `min-height`, `display: block`/`flex`) to prevent squished or unreadable screenshots. Never add decorative inline styles (such as `background`, `border`, `border-radius`, or `padding`) directly onto test harness host tags; let the component's encapsulated SCSS stylesheets govern visual styling and card geometry.
- **Cross-Platform Baseline & `maxDiffPixelRatio`**: Configure `maxDiffPixelRatio` (e.g. `0.05` / 5% in `playwright.config.ts` or spec options) to accommodate Linux/CI subpixel font rendering and anti-aliasing differences across environments while catching genuine layout/color bugs.
- **Snapshot Synchronization**: Baseline PNG snapshots (`.png`) MUST be regenerated and committed alongside spec updates whenever component harness dimensions or HTML host tags change, ensuring identical canvas dimensions between expected and received captures.
- **Strict Component Element Scoping**: Screenshot tests targeting a specific component MUST capture ONLY that specific component or element crop locator (`locator.first().toHaveScreenshot()`), never the entire page or container dashboard (`xmc-resource-dashboard`). Fallbacks or `if/else` conditionals capturing parent containers instead of the target component are strictly forbidden.
- **Isolated Test-Level API Mocking**: Perform all network route mocking strictly within Playwright spec files (`await page.route(...)`) while navigating to production routes (`/resources`). Never add temporary test harness routes or mock data in production routing (`app_routes.ts`) or production source files.


## 9. Angular Signal Immutability & Modern Template Control Flow
- **Signal Mutability Safety**: Prefer Angular Signals (`signal()`, `computed()`) for component state management. NEVER mutate signal objects directly; use immutable update functions (`signal.update(fn)` or `signal.set(newVal)`).
- **Modern Control Flow Syntax**: Use built-in control flow directives (`@if`, `@for`, `@switch`) instead of legacy structural directives (`*ngIf`, `*ngFor`, `*ngSwitch`). Always supply a `track` expression in `@for` loops.

## 10. Declarative Styling Maps
- **Declarative Style Maps**: NEVER write complex inline ternary expressions for dynamic CSS classes or inline styles in HTML templates. Map dynamic styles cleanly in TypeScript component classes using typed `Record<State, string>` styling maps.

## 11. Frontend Security & Bypass Overrides Guardrails
- **Sanitization & Security Overrides**: ALWAYS use Angular's `DomSanitizer` or trusted security helpers when rendering dynamic HTML. Any use of `bypassSecurityTrustHtml()`, `bypassSecurityTrustScript()`, or similar bypass methods MUST include an explicit inline security rationale comment explaining why the content source is verified and safe.
