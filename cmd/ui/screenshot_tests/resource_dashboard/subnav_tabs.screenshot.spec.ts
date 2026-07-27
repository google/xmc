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

import {
  handleUserAuthRoute,
  mountComponentHarness,
  setupScreenshotEnvironment,
} from '../test_utils';

test.use({ viewport: { width: 1280, height: 800 } });

test.describe('SubnavTabsComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for subnav tabs layout', async ({ page }) => {
    await mountComponentHarness(
      page,
      `<xmc-rod-subnav-tabs style="display: block; width: 640px; height: 48px; box-sizing: border-box; background: #ffffff; padding: 8px 16px 0 16px; border-bottom: 1px solid #dadce0; font-family: Roboto, sans-serif;">
        <div style="display: flex; gap: 24px;">
          <button type="button" style="background: none; border: none; border-bottom: 2px solid #1a73e8; padding: 10px 4px; font-size: 14px; font-weight: 500; color: #1a73e8; cursor: pointer;">Smart resource finder</button>
          <button type="button" style="background: none; border: none; border-bottom: 2px solid transparent; padding: 10px 4px; font-size: 14px; font-weight: 500; color: #5f6368; cursor: pointer;">Resource availability</button>
        </div>
      </xmc-rod-subnav-tabs>`,
    );

    const subnavComponent = page.locator('xmc-rod-subnav-tabs').first();
    await expect(subnavComponent).toBeVisible();
    await expect(subnavComponent).toHaveScreenshot('subnav-tabs-component.png');
  });
});
