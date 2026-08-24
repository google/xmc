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

import { expect, test } from '@playwright/test';

import { setupMockRoutes, setupScreenshotEnvironment } from '../test_utils';

test.describe('Resource Dashboard (ROD) Container - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 2400, height: 1200 });
    // Intercept user auth and config API endpoints so authGuard and feature flags succeed
    await setupMockRoutes(page);
    // Navigate to production route /resources
    const jobsPromise = page.waitForResponse(
      (resp) => resp.url().includes('/jobs') && resp.status() === 200,
    );
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await jobsPromise;
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for initial layout and cluster queues', async ({
    page,
  }) => {
    // Capture baseline screenshot of full Resource Dashboard container
    await expect(page).toHaveScreenshot('resource-dashboard-initial.png');
  });

  test('should match baseline screenshot after selecting a cluster queue', async ({ page }) => {
    const dashboard = page.locator('xmc-resource-dashboard');

    // Click on unselected cluster queue item (cpu-cluster-queue) to trigger selection change
    const queueItem = dashboard.locator('xmc-rod-queue-card', {
      hasText: 'cpu-cluster-queue',
    });

    const jobsResponsePromise = page.waitForResponse(
      (resp) => resp.url().includes('/jobs') && resp.status() === 200,
    );
    await queueItem.click();
    await jobsResponsePromise;

    // Capture screenshot with selected queue state
    await expect(page).toHaveScreenshot('resource-dashboard-queue-selected.png');
  });
});
