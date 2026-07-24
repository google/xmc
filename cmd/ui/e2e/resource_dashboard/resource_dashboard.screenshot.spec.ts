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

test.describe('Resource Dashboard (ROD) Container - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    // Intercept user auth API endpoint so authGuard succeeds in e2e browser environment
    await page.route('/api/user', handleUserAuthRoute);
    // Navigate to production route /resources
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
  });

  test('should match baseline screenshot for initial layout and cluster queues', async ({
    page,
  }) => {
    const dashboard = page.locator('xmc-resource-dashboard');
    await expect(dashboard).toBeVisible();

    // Capture baseline screenshot of full Resource Dashboard container
    await expect(dashboard).toHaveScreenshot('resource-dashboard-initial.png');
  });

  test('should match baseline screenshot after selecting a cluster queue', async ({ page }) => {
    const dashboard = page.locator('xmc-resource-dashboard');
    await expect(dashboard).toBeVisible();

    // Click on resolved cluster queue item
    const queueItem = dashboard.locator('.scaffolding-queue-item', {
      hasText: 'gpu-cluster-queue',
    });
    await queueItem.click();

    // Verify selected class is applied
    await expect(queueItem).toHaveClass(/selected/);

    // Capture screenshot with selected queue state
    await expect(dashboard).toHaveScreenshot('resource-dashboard-queue-selected.png');
  });
});
