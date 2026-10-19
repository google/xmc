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

test.describe('Dashboards List Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 950 });
    await setupMockRoutes(page);

    const dashboardsResponsePromise = page.waitForResponse(
      (resp) =>
        /\/dashboards(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/dashboards');
    await page.waitForSelector('xmc-dashboards-list-page');
    await dashboardsResponsePromise;

    await page.locator('xmc-dashboards-list-page table tr.dashboard-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-dashboards-list-page {
          display: flex !important;
          flex-direction: column !important;
          box-sizing: border-box !important;
          height: 900px !important;
          min-height: 900px !important;
          overflow: hidden !important;
        }
      `,
    });
  });

  test('should match baseline screenshot and align cell bottom borders across each row', async ({
    page,
  }) => {
    const listPage = page.locator('xmc-dashboards-list-page');
    await expect(listPage).toBeVisible();

    const tableRows = page.locator('xmc-dashboards-list-page table tr.dashboard-row');
    await expect(tableRows).toHaveCount(2);

    const cellsInFirstRow = tableRows.first().locator('td');
    const cellCount = await cellsInFirstRow.count();
    expect(cellCount).toBe(4);

    const firstCellBox = await cellsInFirstRow.nth(0).boundingBox();
    expect(firstCellBox).not.toBeNull();

    for (let i = 1; i < cellCount; i++) {
      const cellBox = await cellsInFirstRow.nth(i).boundingBox();
      expect(cellBox).not.toBeNull();
      expect(Math.abs(cellBox!.y - firstCellBox!.y)).toBeLessThanOrEqual(1);
      expect(Math.abs(cellBox!.height - firstCellBox!.height)).toBeLessThanOrEqual(1);
    }

    await expect(listPage).toHaveScreenshot('dashboards-list-page.png');
  });
});
