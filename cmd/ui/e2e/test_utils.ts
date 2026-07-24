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

/**
 * Generic base layout reset for E2E component screenshot testing.
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
 */
export async function setupScreenshotEnvironment(page: Page) {
  await page.addStyleTag({
    content: BASE_SCREENSHOT_STYLES,
  });
}

/**
 * Mounts a component HTML harness into the DOM for isolated component screenshot testing.
 */
export async function mountComponentHarness(page: Page, htmlContent: string) {
  await page.evaluate((html) => {
    const container = document.querySelector('.rod-container') || document.body;
    container.innerHTML = html;
  }, htmlContent);
}
