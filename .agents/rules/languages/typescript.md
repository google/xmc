# TypeScript & Modern Angular Guidelines

This document defines architectural and stylistic standards for TypeScript and Angular frontend code in the XManager UI (`cmd/ui/app/`).

---

## 1. TypeScript Strictness & Style
- **Prefer TypeScript**: Write TypeScript (`.ts`) over plain JavaScript (`.js`) across frontend modules and services.
- **Strict Type Checking**: Explicitly type public method signatures, class parameters, and service return types.
- **Default Immutability**: Declare variables as `const` by default. Reserve `let` strictly for required reassignment scopes.

## 2. State Management & Signal Immutability
- **Signal Data Immutability**: Never mutate state objects or arrays returned by Angular signals or RxJS observables in-place.
- **Shallow Copy Requirement**: Create a shallow copy (`[...array]`, `{ ...obj }`) prior to sorting, filtering, or modifying array structures sourced from signals.

```typescript
// ❌ INCOMPLIANT (Mutates internal signal reference directly)
const sorted = this.stateCounts().sort(comparator);

// ✔️ COMPLIANT (Creates shallow copy before sorting)
const sorted = [...this.stateCounts()].sort(comparator);
```

## 3. Modern Angular Standards
- **Built-in Control Flow**: Exclusively use modern block control flow (`@if`, `@for`, `@switch`) over legacy structural directives (`*ngIf`, `*ngFor`).
- **Functional Signal Primitives**: Standardize component inputs, outputs, models, and queries to functional signal primitives (`input()`, `output()`, `model()`, `viewChild()`).
- **Template Access Encapsulation**: Use `protected` visibility for class members accessed exclusively by HTML templates.
- **Declarative Style Maps**: Extract UI styling logic into declarative dictionary `Map` objects.
- **Standalone Architecture**: Ensure components, directives, and pipes are standalone (`standalone: true`) by default.

## 4. Dependency Injection (DI) & Mocking
- **No Embedded Mock Data**: Never embed static mock datasets or `if (useMockData)` branches in production Angular service classes.
- **DI Factory Providers**: Register DI factory providers in application configuration (`app_config.ts`) to resolve mock vs production implementations dynamically.
