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

test.describe('QueueListComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for queue list section', async ({ page }) => {
    await mountComponentHarness(
      page,
      `<xmc-rod-queue-list style="display: block; width: 600px; min-height: 200px; box-sizing: border-box; padding: 16px; background: #ffffff; border: 1px solid #e0e0e0; border-radius: 8px;"></xmc-rod-queue-list>`,
    );

    const queueListComponent = page.locator('xmc-rod-queue-list').first();
    await expect(queueListComponent).toBeVisible();
    await expect(queueListComponent).toHaveScreenshot('queue-list-component.png', {
      maxDiffPixelRatio: 0.05,
    });
  });
});
