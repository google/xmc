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

import { mountComponentHarness, setupMockRoutes, setupScreenshotEnvironment } from '../test_utils';

test.use({ viewport: { width: 1600, height: 900 } });

test.describe('QueueJobsTableComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await setupMockRoutes(page);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for queue jobs table section', async ({ page }) => {
    await page.evaluate(() => {
      const pane = document.querySelector('.jobs-side-pane') as HTMLElement;
      if (pane) pane.style.width = '1000px';
      const tbl = document.querySelector('xmc-queue-jobs-table') as HTMLElement;
      if (tbl) tbl.style.width = '1000px';
    });

    await mountComponentHarness(
      page,
      `<xmc-queue-jobs-table style="display: block; width: 1000px; min-height: 250px;"></xmc-queue-jobs-table>`,
    );

    const tableComponent = page.locator('xmc-queue-jobs-table').first();
    await expect(tableComponent).toBeVisible();
    await expect(tableComponent).toHaveScreenshot('queue-jobs-table-component.png', {
      maxDiffPixelRatio: 0.05,
    });
  });
});
