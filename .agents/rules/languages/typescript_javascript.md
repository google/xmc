# TypeScript & Modern Angular UI Guidelines

This document defines core architectural, structural, and stylistic standards for TypeScript and Angular frontend code in the XManager UI (`cmd/ui/app/`), incorporating modern Angular architecture and clean code best practices.

---

## 1. TypeScript Strictness & Style
- **Prefer TypeScript over JavaScript**: Prefer writing TypeScript (`.ts`) over plain JavaScript (`.js`) whenever viable across frontend modules, services, and web tooling.
- **UI & Workflow File Naming Conventions**: Angular component templates use `*.ng.html` (never `*.html`, apart from the root [`cmd/ui/index.html`](../../../cmd/ui/index.html)), Angular Karma unit test specs use `*_test.ts` (never `*.spec.ts`), Playwright visual regression specs under [`cmd/ui/screenshot_tests/`](../../../cmd/ui/screenshot_tests/) use `*.screenshot.spec.ts`, and Node workflow script tests under [`.github/scripts/`](../../../.github/scripts/) use `*.test.js` (enforced by `tools/lint/check_file_naming.py`).
- **TypeScript Strictness**: Enforce strict type checking across all files. Explicitly type public method signatures, class parameters, and service return types.
- **Avoid `any` & Prefer Precise Typings**: Avoid using the `any` escape hatch in TypeScript interfaces, function parameters, state objects, and return types unless strictly necessary (such as legacy third-party untyped library interop). Prefer explicit domain types, generic type parameters, narrow union types, or `unknown` (with explicit type narrowing) over `any` to preserve compile-time type safety.
- **Default Immutability (`const` over `let`)**: Follow default immutability standards defined in [Core Engineering Standards](../core_engineering.md#3-default-immutability--safe-data-handling). Declare variables as `const` by default, reserving `let` strictly for scopes where reassignment is structurally required.
- **Constructive Initialization**: Use ternary expressions (`cond ? a : b`), object/array spread syntax (`...`), and default nullish fallbacks (`||`, `??`) to eliminate imperative variable reassignment chains.
- **Clean Import Tree**: Avoid imports utilizing more than two parent directory traversals (`../../../`). Use qualified project TypeScript path mapping aliases (e.g., `app/...`) or localized flat relative paths (`./models`, `./resource_filter`).
- **Protobuf-to-TypeScript Interface Cleanliness**: In ambient TypeScript declaration stubs (*.d.ts) and UI view models, map 32-bit numeric Protobuf schema fields (int32, uint32, float, double) strictly to clean number types, and 64-bit fields (int64, uint64) to bigint (or number if values are guaranteed to be within safe integer limits). Do NOT use defensive number | string type unions in UI interfaces unless the field is explicitly declared as a string in the canonical .proto definition. Keep network transport string quirks isolated to service/JSON parsing layers.

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
- **Eradicate Embedded Mock Data & Fixtures**: Follow DI mock decoupling standards defined in [Core Engineering Standards](../core_engineering.md#4-high-fidelity-test-doubles--decoupling). Never import static mock datasets, define hardcoded sample entity arrays (`createDefaultCharts()`), or embed conditional mock execution/fallback branches inside production Angular components or service classes.
- **Standalone Mock Classes & Reusable Test Fixtures**: Move all mock implementations and stubbed responses into dedicated standalone mock service classes (e.g., `MockExperimentService extends ExperimentService`) or reusable fixture helpers (e.g., `cmd/ui/app/testing/mock_dashboards.ts`) located strictly in dedicated testing directories (e.g., `service/testing/` or `app/testing/`).
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

## 7. Angular Component Testing, Cleanstate & DOM Interaction Standards
- **Install Dependencies Before UI Tests & Lint**: A fresh checkout or git worktree has no installed UI dependencies (no `node_modules` directory). Run `npm ci --legacy-peer-deps` in `cmd/ui` first, as `.github/actions/run-ui-tests` does. Then run `npm test -- --watch=false --browsers=ChromeHeadlessNoSandbox`, `npm run lint`, or `bazel test //cmd/ui/...` ([`cmd/ui/BUILD.bazel`](../../../cmd/ui/BUILD.bazel) is a single root Bazel package exposing `//cmd/ui:ui_unit_tests`—there are no sub-package `BUILD.bazel` files under `cmd/ui/app/`).
- **Cleanstate Helper vs Mutable `let` State**: Adopt the codebase standard `cleanState`, `setupModule`, and `bootstrap` helpers from `app/testing/test` across Angular Jasmine test suites. Never declare top-level mutable `let fixture`, `let component`, or spy variables at the `describe` closure level; initializing state inside `cleanState(...)` ensures automatic `afterEach` fixture destruction (`fixture.destroy()`) and complete test isolation without memory leaks.
- **Hermetic DI in Tests**: Always provide test doubles and mock services (e.g. `SnackbarService`, `Clipboard`, `MatDialog`) through `TestBed.configureTestingModule` / `setupModule` providers rather than casting components to `unknown` and monkey-patching private properties.
- **Mandatory DOM Interaction Coverage**: Every Angular component test suite MUST test all interactive template workflows:
  - Dynamic list additions/deletions (e.g. clicking add/remove buttons and asserting resulting DOM element counts).
  - Form control value updates (`input`, `change` events) and boundary validation (invalid/negative inputs).
  - Output signal emissions following user click actions.
- **Prefer Component Harnesses & Top-Level DOM Selector Constants**: Prioritize using Angular CDK `ComponentHarness` classes for DOM interactions and assertions to insulate tests from template changes. When harnesses are not available or practical, never repeat raw CSS query strings (`.my-btn`, `#my-input`) inside test bodies; extract all DOM selectors to named constants at the top of the test file:
  ```typescript
  const AMOUNT_INPUT_SELECTOR = '#amount-input';
  const ADD_BTN_SELECTOR = '.add-resource-btn';
  ```

### Angular CDK Component Harness Standards
- **100% Harness Encapsulation**: Test specs must interact with components strictly via Component Harnesses (`@angular/cdk/testing`). Avoid defining raw CSS selectors or directly querying component DOM elements (`getEl`, `querySelector`) inside test specs.
- **DRY in Harnesses**: Harness methods querying sub-elements or table cells must extract shared traversal helpers (e.g. `getDataRows()`, `getUrlsCell()`) rather than repeating table/row/cell locator chains in each method.

## 8. Playwright Visual Screenshot Testing
- **Full-Page Screenshot Tests**: Playwright visual screenshot tests MUST ONLY be created for full top-level page views and container routes (e.g. `cmd/ui/screenshot_tests/resource_dashboard/resource_dashboard.screenshot.spec.ts` for `/resources`, `cmd/ui/screenshot_tests/admin_panel/admin_panel.screenshot.spec.ts` for `/admin`). Do NOT create individual component-level screenshot tests for sub-components, child widgets, or isolated cards (such as queue cards, subnav tabs, or side panel table components).
- **Shared Screenshot Test Harness**: Page visual screenshot tests MUST rely on the shared test utilities (`setupMockRoutes` and `setupScreenshotEnvironment` in `test_utils.ts`).
- **Cross-Platform Baseline & `maxDiffPixelRatio`**: Configure `maxDiffPixelRatio` (e.g. `0.05` / 5% in `playwright.config.ts` or spec options) to accommodate Linux/CI subpixel font rendering and anti-aliasing differences across environments while catching genuine layout/color bugs.
- **Snapshot Synchronization**: Baseline PNG snapshots (`.png`) MUST be regenerated and committed alongside spec updates whenever page container layouts change, ensuring identical canvas dimensions between expected and received captures.
- **Full Page Element Scoping**: Screenshot tests MUST target top-level page container element locators (`page.locator('xmc-resource-dashboard')` or `page.locator('xmc-admin-panel')`), capturing complete page views rather than isolated sub-elements.
- **Asynchronous & Synchronous Element Synchronization**: Screenshot assertions (`toHaveScreenshot()`) MUST explicitly wait for all synchronous and asynchronous page elements, cards, and data-dependent widgets (e.g. `await page.locator('xmc-config-entries-card').waitFor({ state: 'visible' })` or `await expect(card).toBeVisible()`) to load and render fully in the DOM before taking a screenshot. Never rely solely on early page container visibility or header text, as pending asynchronous API calls or Signal updates will cause image height discrepancies and flakiness between environments.
- **Isolated Test-Level API Mocking**: Perform all network route mocking strictly within Playwright spec files (`await page.route(...)` or via `setupMockRoutes()`) while navigating to production routes (`/resources`, `/admin`). Never add temporary test harness routes or mock data in production routing (`app_routes.ts`) or production source files.


## 9. Angular Signal Immutability & Modern Template Control Flow
- **Signal Mutability Safety**: Prefer Angular Signals (`signal()`, `computed()`) for component state management. NEVER mutate signal objects directly; use immutable update functions (`signal.update(fn)` or `signal.set(newVal)`).
- **Modern Control Flow Syntax**: Use built-in control flow directives (`@if`, `@for`, `@switch`) instead of legacy structural directives (`*ngIf`, `*ngFor`, `*ngSwitch`). Always supply a `track` expression in `@for` loops.

## 10. Declarative Styling Maps
- **Declarative Style Maps**: NEVER write complex inline ternary expressions for dynamic CSS classes or inline styles in HTML templates. Map dynamic styles cleanly in TypeScript component classes using typed `Record<State, string>` styling maps.

## 11. Frontend Security & Bypass Overrides Guardrails
- **Sanitization & Security Overrides**: ALWAYS use Angular's `DomSanitizer` or trusted security helpers when rendering dynamic HTML. Any use of `bypassSecurityTrustHtml()`, `bypassSecurityTrustScript()`, or similar bypass methods MUST include an explicit inline security rationale comment explaining why the content source is verified and safe.
