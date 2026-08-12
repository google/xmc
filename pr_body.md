## Summary
This PR enforces rigorous Angular strictness inside the UI layer, preventing legacy `@Input()` and `@Output()` usage in favor of modern signal-based primitives. It also removes `any` masking exceptions, increases layout scrutiny, and resolves pre-existing state tracking stream drift.

## Motivation & Context
Aligning with Google's modern Angular constraints reduces rendering lifecycle complexity and ensures signals can optimally track fine-grained updates. The escalation of complexity/lines-per-function metrics forces structural hygiene inside the core UI without permitting implicit variable masking tricks.

## Key Changes
- Elevates `max-lines-per-function` and `complexity` to `error` level for `.ts` files inside `cmd/ui/eslint.config.js`.
- Introduces aggressive `@typescript-eslint/no-explicit-any`, `no-inputs-metadata-property`, and `no-outputs-metadata-property` constraint bans across production TS packages.
- Configures custom `no-restricted-syntax` bans on UI Decorators (`@Input`, `@Output`, `@ViewChild`) pointing developers toward their `input()`, `output()`, and `viewChild()` primitive counterparts.
- Defuses structural limits in `getArtifactDisplayProperties` via explicit inline override to preserve discriminated union TS bounds (`switch` statements), rather than defaulting to `any`.
- Wraps `groupingThreshold` signal dependency with RxJS `combineLatest` + `toObservable` across the `createPhaseStream` mappings to guarantee dynamic upstream responsiveness.

## Verification & Testing
- **Commands Executed**: `cd cmd/ui && npm run lint`
- **Coverage & Test Status**: Angular strict linters return clean (0 errors, 0 warnings). The `artifacts_list` and `artifact_utils` TS logic adjustments cleanly pass all current automated tests.

## Related Issues / Bugs
Fixes N/A

TAG=agy
CONV=ae07c273-e171-412f-9e6b-7c662297ca6b
