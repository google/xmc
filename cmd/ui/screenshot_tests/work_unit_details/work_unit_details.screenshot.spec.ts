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

test.describe('WorkUnit Details Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1200 });
    await setupMockRoutes(page);

    const expResponsePromise = page.waitForResponse(
      (resp) =>
        /\/experiments\/10293841(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );
    const workUnitResponsePromise = page.waitForResponse(
      (resp) =>
        /\/workUnits\/1(?:\?.*)?$/.test(resp.url()) &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/experiments/10293841/workUnits/1');
    await page.waitForSelector('xmc-work-unit-page');
    await expResponsePromise;
    await workUnitResponsePromise;

    // Wait for header and jobs table to render
    await page.locator('xmc-work-unit-header').waitFor({ state: 'visible' });
    await page.locator('xmc-work-unit-jobs-table table tr.mat-mdc-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-work-unit-page {
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

  test('should match baseline screenshot for the WorkUnit Details page (Jobs tab)', async ({
    page,
  }) => {
    const wuPage = page.locator('xmc-work-unit-page');
    await expect(wuPage).toBeVisible();

    const title = page.locator('.work-unit-title-container h1');
    await expect(title).toHaveText('Work Unit 1');

    const jobRows = page.locator('xmc-work-unit-jobs-table table tr.mat-mdc-row');
    await expect(jobRows).toHaveCount(2);

    await expect(wuPage).toHaveScreenshot('work-unit-details-page.png');
  });

  test('should match baseline screenshot for the WorkUnit Artifacts tab', async ({ page }) => {
    const wuPage = page.locator('xmc-work-unit-page');
    await expect(wuPage).toBeVisible();

    // Switch to Artifacts tab
    const artifactsTab = page.getByRole('tab', { name: 'Artifacts' });
    await artifactsTab.click();

    await page.locator('xmc-artifacts-list .pipeline-container').waitFor({ state: 'visible' });
    await page
      .locator('xmc-artifacts-list xmc-artifact-card')
      .first()
      .waitFor({ state: 'visible' });

    await expect(wuPage).toHaveScreenshot('work-unit-details-artifacts-tab.png');
  });
});
