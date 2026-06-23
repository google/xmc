# Core Engineering & Multi-Agent Coordination Guidelines

This document provides foundational architectural, structural, and clean code guidelines for all XManager Google development.

---

## 1. Single Responsibility & Linear Execution
- **Keep Functions Focused**: Break monolithic, multi-purpose functions into localized, pure helper methods. 
- **Method Length**: Aim to keep functions and methods within a highly readable 20-30 line cap.
- **Linear Indentation**: Keep control flow completely linear. Eliminate unneeded nested `if/else` chains by leveraging early `return` guard clauses and dropping `else` blocks that follow `return` or `throw` terminations.

## 2. Default Immutability (`const` over `let`)
- **Strict `const` Usage**: Declare all identifiers as `const` by default.
- **Scoped `let` Variables**: Reserve `let` variables strictly for instances where localized value reassignment is an explicit, unavoidable structural requirement (e.g., loop index counters, accumulators, specific flags).
- **Constructive Initialization**: For variable properties that depend on configuration state or conditional expressions, use object spread operators (`...`), ternary operators (`cond ? a : b`), or fallback defaults (`||`, `??`) rather than setting a generic `let` and running imperative inline assignments.

## 3. High-Fidelity Test Doubles
- **Mock Interfaces**: Ensure all mock execution models rigorously match the real structural requirements of the underlying production infrastructure.
- **Parametric Order Matching**: For HTTP or service mocking layers (like Angular's `HttpTestingController`), ensure query parameter maps (`HttpParams`) are serialized in the exact property ordering expected by matching criteria strings.
- **Avoid Hallucinated Identifiers**: When writing unit test harnesses or expanding mock verification suites, do not introduce hardcoded mock identifiers (e.g., `'123'`) that conflict with existing base mock databases (e.g., `'experiments/1'`). 

## 4. DI Polymorphism & Mock Decoupling
- **Eradicate Embedded Mock Data**: Never embed static mock data imports or conditional mock execution branches inside production application code or service layers. Production classes must remain 100% pure real execution logic.
- **Polymorphic Provisioning**: Extract all stubbed mock execution into standalone classes (located in isolated testing directories). Register factory providers in core application configs to dynamically resolve implementations at runtime using Dependency Injection (DI) rather than inline branching. This completely decouples testing payloads from production artifacts.
