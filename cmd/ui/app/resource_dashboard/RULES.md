<!--
 Copyright 2026 Google LLC

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
-->

# Resource Observability Dashboard (ROD) Agent Rules

This document defines mandatory development and testing standards for all Angular UI components under `cmd/ui/app/resource_dashboard/`.

---

## Mandatory Screenshot Testing for Angular Components

Whenever adding, extending, or modifying Angular components in `cmd/ui/app/resource_dashboard/` (or subdirectories such as `components/`):

1. **Mandatory Playwright Screenshot Test Coverage**:
   - Every Angular component MUST have a corresponding Playwright visual screenshot test spec located under `cmd/ui/e2e/resource_dashboard/` (e.g. `cmd/ui/e2e/resource_dashboard/<component_name>.screenshot.spec.ts`).
   - Use Playwright assertions (`expect(locator).toHaveScreenshot(...)`) to capture baseline visual snapshots of the component layout and state variations.

2. **Isolated Test-Level API Mocking (Zero Production Changes)**:
   - Perform all network route mocking strictly within the Playwright spec file (`await page.route('/api/user', ...)`) while navigating directly to production routes (e.g. `/resources`).
   - **NEVER** add temporary test harness routes, mock routing data, or conditional test logic inside production Angular routing (`app_routes.ts`) or production source files (`.ts`, `.ng.html`).

3. **Running and Updating Baselines**:
   - **Run Screenshot Tests**:
     ```bash
     cd cmd/ui
     npm run test:screenshot
     ```
   - **Update Baseline Screenshots**:
     ```bash
     cd cmd/ui
     npm run test:screenshot:update
     ```
