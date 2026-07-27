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

test.describe('QueueCardComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for queue card section', async ({ page }) => {
    await mountComponentHarness(
      page,
      `<xmc-rod-queue-card style="display: block; width: 440px; height: 204px; box-sizing: border-box; padding: 16px; background: #ffffff; border: 1px solid #e0e0e0; border-radius: 8px; font-family: Roboto, sans-serif;">
        <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 12px;">
          <div>
            <h3 style="margin: 0; font-size: 16px; font-weight: 500; color: #202124;">gpu-cluster-queue</h3>
            <span style="font-size: 12px; color: #5f6368;">Cohort: prod-gpu</span>
          </div>
          <div style="display: flex; align-items: center; gap: 6px; background: #e6f4ea; color: #137333; padding: 4px 10px; border-radius: 12px; font-size: 12px; font-weight: 500;">
            <span style="width: 8px; height: 8px; background: #137333; border-radius: 50%;"></span>
            Healthy
          </div>
        </div>
        <div style="margin-bottom: 12px;">
          <div style="font-size: 12px; font-weight: 500; color: #5f6368; margin-bottom: 6px;">Capacity & Predictability</div>
          <div style="border: 1px solid #f1f3f4; border-radius: 4px; padding: 8px; font-size: 12px;">
            <div style="display: flex; justify-content: space-between; font-weight: 500; color: #5f6368; padding-bottom: 4px; border-bottom: 1px solid #f1f3f4;">
              <span>Resource</span>
              <span>Max workload</span>
              <span>Free / Total</span>
            </div>
            <div style="display: flex; justify-content: space-between; padding-top: 6px; color: #202124;">
              <span style="font-weight: 500;">nvidia-h100</span>
              <span>64 GPUs</span>
              <span>32 / 128 GPUs</span>
            </div>
          </div>
        </div>
        <div style="display: flex; align-items: center; gap: 8px; background: #e8f0fe; color: #1a73e8; padding: 8px 12px; border-radius: 4px; font-size: 12px; font-weight: 500;">
          <span>✓ High likelihood of immediate scheduling</span>
        </div>
      </xmc-rod-queue-card>`,
    );

    const queueCardComponent = page.locator('xmc-rod-queue-card').first();
    await expect(queueCardComponent).toBeVisible();
    await expect(queueCardComponent).toHaveScreenshot('queue-card-component.png');
  });
});
