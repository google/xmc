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

import { MOCK_EXPERIMENTS_LIST, setupMockRoutes, setupScreenshotEnvironment } from '../test_utils';

test.describe('Experiments List Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 950 });
    await setupMockRoutes(page);

    const experimentsResponsePromise = page.waitForResponse(
      (resp) =>
        /\/experiments(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/experiments');
    await page.waitForSelector('xmc-experiments-list-page');
    await experimentsResponsePromise;

    // Ensure table rows and status badges are rendered
    await page.locator('xmc-experiments-table table tr.mat-mdc-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-experiments-list-page {
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

  test('should match baseline screenshot for the entire Experiments List page', async ({
    page,
  }) => {
    const listPage = page.locator('xmc-experiments-list-page');
    await expect(listPage).toBeVisible();

    const tableRows = page.locator('xmc-experiments-table table tr.mat-mdc-row:not(.detail-row)');
    await expect(tableRows).toHaveCount(4);

    await expect(listPage).toHaveScreenshot('experiments-list-page.png');
  });

  for (const [name, message] of [
    ['long error text', 'Failed to create work unit: insufficient resources. '.repeat(80)],
    ['unbroken error text', `Failed to load /${'very_long_resource_name_'.repeat(200)}`],
  ]) {
    test(`should wrap ${name} without widening the table`, async ({ page }) => {
      const table = page.locator('xmc-experiments-table table');
      const errorText = table.locator('xmc-status-message .error-message .text');
      await expect(errorText).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const originalWidth = await table.evaluate(
        (element) => element.getBoundingClientRect().width,
      );

      await page.route(/\/experiments(?:\?.*)?$/, async (route) => {
        if (route.request().resourceType() === 'document') {
          await route.fallback();
          return;
        }
        await route.fulfill({
          json: {
            ...MOCK_EXPERIMENTS_LIST,
            experiments: MOCK_EXPERIMENTS_LIST.experiments?.map((experiment) => ({
              ...experiment,
              status: {
                ...experiment.status,
                structuredStatusMessage: {
                  ...experiment.status?.structuredStatusMessage,
                  userFacingText: message,
                },
              },
            })),
          },
        });
      });
      await page.getByRole('columnheader', { name: 'Created' }).click();

      await expect(errorText).toHaveText(message.trim());
      expect(
        await table.evaluate((element) => element.getBoundingClientRect().width),
      ).toBeLessThanOrEqual(originalWidth + 1);
      expect(
        await errorText.evaluate((element) => element.scrollWidth - element.clientWidth),
      ).toBeLessThanOrEqual(1);
    });
  }
});
