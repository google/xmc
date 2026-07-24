// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import type { Page, Route } from '@playwright/test';

export const MOCK_USER = {
  email: 'test@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

export interface ScreenshotOptions {
  /** Target element selector (e.g. 'xmc-new-component') */
  selector?: string;
  /** Optional explicit width override in pixels or CSS units */
  width?: number | string;
  /** Optional explicit height override in pixels or CSS units */
  height?: number | string;
}

/**
 * Generic base layout reset and wildcard component styling for E2E screenshot testing.
 * Automatically formats viewports, containers, custom elements, and placeholders.
 */
const BASE_SCREENSHOT_STYLES = `
  html, body {
    width: 1280px !important;
    margin: 0 !important;
    padding: 0 !important;
  }
  [class*="-container"] {
    width: 1216px !important;
    margin: 0 auto !important;
  }
  xmc-*,
  [data-screenshot-target],
  [class*="placeholder"],
  [class*="scaffolding"] {
    display: block !important;
    box-sizing: border-box !important;
  }
`;

export async function handleUserAuthRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER),
  });
}

/**
 * Prepares the Playwright page runtime for deterministic component screenshot assertions.
 * Automatically injects base reset styles and allows optional custom bounding overrides.
 */
export async function setupScreenshotEnvironment(page: Page, options?: ScreenshotOptions) {
  await page.addStyleTag({
    content: BASE_SCREENSHOT_STYLES,
  });

  if (options?.selector && (options.width != null || options.height != null)) {
    const formatValue = (val: number | string) => (typeof val === 'number' ? `${val}px` : val);

    const widthStyle =
      options.width != null
        ? `width: ${formatValue(options.width)} !important; min-width: ${formatValue(options.width)} !important; max-width: ${formatValue(options.width)} !important;`
        : '';

    const heightStyle =
      options.height != null
        ? `height: ${formatValue(options.height)} !important; min-height: ${formatValue(options.height)} !important; max-height: ${formatValue(options.height)} !important;`
        : '';

    await page.addStyleTag({
      content: `${options.selector} { ${widthStyle} ${heightStyle} }`,
    });
  }
}
