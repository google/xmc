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

test.describe('SmartResourceFinderComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for smart resource finder section', async ({ page }) => {
    await mountComponentHarness(
      page,
      `<xmc-smart-resource-finder style="display: flex; align-items: center; gap: 16px; width: 960px; height: 88px; box-sizing: border-box; padding: 16px 24px; background: #ffffff; border: 1px solid #e0e0e0; border-radius: 8px; font-family: Roboto, sans-serif;">
        <span style="font-weight: 500; color: #202124; font-size: 14px;">I need</span>
        <div style="display: flex; flex-direction: column; gap: 4px;">
          <label style="font-size: 12px; color: #5f6368; font-weight: 500;">Amount *</label>
          <input type="number" value="64" style="width: 80px; padding: 8px 10px; border: 1px solid #dadce0; border-radius: 4px; font-size: 14px;" />
        </div>
        <div style="display: flex; flex-direction: column; gap: 4px;">
          <label style="font-size: 12px; color: #5f6368; font-weight: 500;">Resource type *</label>
          <select style="width: 220px; padding: 8px 10px; border: 1px solid #dadce0; border-radius: 4px; font-size: 14px; background: #fff;">
            <option>All resource types (Default)</option>
            <option selected>NVIDIA H100 GPU</option>
          </select>
        </div>
        <button type="button" style="background: none; border: none; color: #1a73e8; font-weight: 500; font-size: 14px; cursor: pointer; padding: 8px 12px;">+ Add resource type</button>
        <button type="button" style="background: #1a73e8; color: #ffffff; border: none; border-radius: 4px; padding: 10px 20px; font-weight: 500; font-size: 14px; cursor: pointer; display: flex; align-items: center; gap: 8px; margin-left: auto;">Search queues</button>
      </xmc-smart-resource-finder>`,
    );

    const smartFinderComponent = page.locator('xmc-smart-resource-finder').first();
    await expect(smartFinderComponent).toBeVisible();
    await expect(smartFinderComponent).toHaveScreenshot('smart-resource-finder-initial.png');
  });
});
