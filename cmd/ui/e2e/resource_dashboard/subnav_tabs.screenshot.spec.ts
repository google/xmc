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

test.describe('SubnavTabsComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 2400, height: 1080 });
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
  });

  test('should match baseline screenshot for subnav tabs layout', async ({ page }) => {
    const subnavComponent = page.locator('xmc-rod-subnav-tabs, .rod-subnav-placeholder').first();
    await expect(subnavComponent).toBeAttached();
    await subnavComponent.evaluate((el) => {
      (el as HTMLElement).style.width = '1976px';
      (el as HTMLElement).style.height = '48px';
      (el as HTMLElement).style.display = 'block';
      (el as HTMLElement).style.boxSizing = 'border-box';
    });
    await expect(subnavComponent).toHaveScreenshot('subnav-tabs-component.png');
  });
});
