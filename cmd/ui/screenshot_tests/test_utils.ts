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
 * Generic base layout reset for component screenshot testing.
 */
const BASE_SCREENSHOT_STYLES = `
  html, body {
    width: 1600px !important;
    margin: 0 !important;
    padding: 0 !important;
  }
  [class*="-container"] {
    width: 1536px !important;
    margin: 0 auto !important;
  }
  xmc-*,
  [data-screenshot-target],
  [class*="placeholder"],
  [class*="scaffolding"] {
    display: block !important;
    box-sizing: border-box !important;
  }
  xmc-rod-subnav-tabs {
    height: 48px !important;
  }
  xmc-smart-resource-finder {
    height: 88px !important;
  }
  xmc-rod-queue-card {
    height: 360px !important;
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
    const tempDiv = document.createElement('div');
    tempDiv.innerHTML = html;
    const targetElement = tempDiv.firstElementChild;
    const container = document.querySelector('.rod-container') || document.body;

    if (targetElement) {
      const tagName = targetElement.tagName.toLowerCase();
      const liveElement = document.querySelector(tagName);
      if (liveElement && !targetElement.innerHTML.trim()) {
        targetElement.innerHTML = liveElement.innerHTML;
      }
    }
    container.innerHTML = tempDiv.innerHTML;
  }, htmlContent);
}
