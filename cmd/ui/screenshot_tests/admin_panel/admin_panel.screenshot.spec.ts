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

test.describe('Admin Panel Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1350 });
    await setupMockRoutes(page);

    // Go to admin panel
    await page.goto('/admin');
    await page.waitForSelector('xmc-admin-panel');
    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-admin-panel {
          display: block !important;
          box-sizing: border-box !important;
          height: 1300px !important;
          min-height: 1300px !important;
          overflow: hidden !important;
        }
      `,
    });
  });

  test('should match baseline screenshot for the entire Admin Panel page', async ({ page }) => {
    const adminPanel = page.locator('xmc-admin-panel');
    await expect(adminPanel).toBeVisible();

    const footer = page.locator('.admin-panel-footer');
    await expect(footer).toContainText('Build Version: v1.5.1-build.42');

    // Wait for asynchronous config entries card and its table rows to render fully
    const configRow = page.locator('xmc-config-entries-card .config-row').first();
    await expect(configRow).toBeVisible();

    // Take screenshot of the entire admin panel page
    await expect(adminPanel).toHaveScreenshot('admin-panel-page.png');
  });
});
