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

test.describe('Experiment Details Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1200 });
    await setupMockRoutes(page);

    const expResponsePromise = page.waitForResponse(
      (resp) =>
        /\/experiments\/10293841(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );
    const workUnitsResponsePromise = page.waitForResponse(
      (resp) =>
        /\/experiments\/10293841\/workUnits(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/experiments/10293841');
    await page.waitForSelector('xmc-experiment-page');
    await expResponsePromise;
    await workUnitsResponsePromise;

    // Ensure metadata card and work units table rows render
    await page.locator('.experiment-summary-metadata').waitFor({ state: 'visible' });
    await page.locator('xmc-work-units-table table tr.mat-mdc-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-experiment-page {
          display: flex !important;
          flex-direction: column !important;
          box-sizing: border-box !important;
          height: 1150px !important;
          min-height: 1150px !important;
          overflow: hidden !important;
        }
      `,
    });
  });

  test('should match baseline screenshot for the Experiment Details page (Work Units tab)', async ({
    page,
  }) => {
    const expPage = page.locator('xmc-experiment-page');
    await expect(expPage).toBeVisible();

    const headerTitle = page.locator('.experiment-title');
    await expect(headerTitle).toHaveText('Gemini 1.5 Pro Fine-Tuning - Run 42');

    const workUnitRows = page.locator('xmc-work-units-table table tr.mat-mdc-row:not(.detail-row)');
    await expect(workUnitRows).toHaveCount(4);

    await expect(expPage).toHaveScreenshot('experiment-details-page.png');
  });

  test('should match baseline screenshot for the Artifacts tab', async ({ page }) => {
    const expPage = page.locator('xmc-experiment-page');
    await expect(expPage).toBeVisible();

    // Switch to Artifacts tab
    const artifactsTab = page.getByRole('tab', { name: 'Artifacts' });
    await artifactsTab.click();

    await page.locator('xmc-artifacts-list .pipeline-container').waitFor({ state: 'visible' });
    await page
      .locator('xmc-artifacts-list xmc-artifact-card')
      .first()
      .waitFor({ state: 'visible' });

    await expect(expPage).toHaveScreenshot('experiment-details-artifacts-tab.png');
  });
});
