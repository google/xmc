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

test.use({ viewport: { width: 2200, height: 900 } });

test.describe('QueueJobsTableComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await setupMockRoutes(page);
    await page.route('**/clusterqueues/*/jobs', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          jobs: [
            {
              jobName: 'xmc-operation-53296-1',
              xid: '53296',
              user: 'alice@google.com',
              priority: 'HIGH',
              status: 'RUNNING',
              positionInQueue: 1,
              workUnitId: '101',
            },
            {
              jobName: 'xmc-operation-53308-1',
              xid: '53308',
              user: 'bob@google.com',
              priority: 'NORMAL',
              status: 'RUNNING',
              positionInQueue: 2,
              workUnitId: '102',
            },
          ],
          totalQueueSize: 2,
        }),
      });
    });
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for queue jobs table section', async ({ page }) => {
    await page.evaluate(() => {
      const pane = document.querySelector('.jobs-side-pane') as HTMLElement;
      if (pane) {
        pane.style.flex = 'none';
        pane.style.width = '2000px';
      }
      const tbl = document.querySelector('xmc-queue-jobs-table') as HTMLElement;
      if (tbl) tbl.style.width = '2000px';
    });

    await mountComponentHarness(
      page,
      `<xmc-queue-jobs-table style="display: block; width: 2000px; min-height: 250px;"></xmc-queue-jobs-table>`,
    );

    const tableComponent = page.locator('xmc-queue-jobs-table').first();
    await expect(tableComponent).toBeVisible();
    await expect(tableComponent).toHaveScreenshot('queue-jobs-table-component.png', {
      maxDiffPixelRatio: 0.05,
    });
  });
});
