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

import type { Route } from '@playwright/test';
import { expect, test } from '@playwright/test';

const MOCK_USER = {
  email: 'test@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

async function handleUserAuthRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER),
  });
}

test.use({ viewport: { width: 1280, height: 800 } });

test.describe('SubnavTabsComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await page.addStyleTag({
      content: `
        html, body { width: 1280px !important; margin: 0 !important; padding: 0 !important; }
        .rod-container { width: 1216px !important; max-width: 1216px !important; min-width: 1216px !important; margin: 0 auto !important; }
        xmc-rod-subnav-tabs, .rod-subnav-placeholder {
          display: block !important;
          width: 1216px !important;
          min-width: 1216px !important;
          max-width: 1216px !important;
          height: 48px !important;
          min-height: 48px !important;
          max-height: 48px !important;
          box-sizing: border-box !important;
        }
      `,
    });
  });

  test('should match baseline screenshot for subnav tabs layout', async ({ page }) => {
    const subnavComponent = page.locator('xmc-rod-subnav-tabs, .rod-subnav-placeholder').first();
    await expect(subnavComponent).toBeAttached();
    await expect(subnavComponent).toHaveScreenshot('subnav-tabs-component.png');
  });
});
