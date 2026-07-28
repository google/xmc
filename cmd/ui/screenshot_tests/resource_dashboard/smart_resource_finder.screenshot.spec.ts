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

import { handleUserAuthRoute, setupScreenshotEnvironment } from '../test_utils';

test.use({ viewport: { width: 1280, height: 800 } });

test.describe('SmartResourceFinderComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for smart resource finder section', async ({ page }) => {
    const smartFinderComponent = page.locator('xmc-smart-resource-finder').first();
    await smartFinderComponent.evaluate((el) => {
      el.style.cssText =
        'display: flex; align-items: center; gap: 16px; width: 960px; height: 88px; box-sizing: border-box; padding: 16px 24px; background: #ffffff; border: 1px solid #e0e0e0; border-radius: 8px;';
    });
    await expect(smartFinderComponent).toBeVisible();
    await expect(smartFinderComponent).toHaveScreenshot('smart-resource-finder-initial.png', {
      maxDiffPixelRatio: 0.05,
    });
  });
});
