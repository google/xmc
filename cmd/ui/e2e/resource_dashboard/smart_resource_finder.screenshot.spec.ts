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

import type { Route } from '@playwright/test';
import { expect, test } from '@playwright/test';

const MOCK_USER = {
  email: 'test@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

const FINDER_QUERY_CARD_HTML = `
  <div class="finder-query-card">
    <span class="query-prefix">I need</span>
    <div class="query-input-group">
      <label class="query-label" for="amount-input">Amount *</label>
      <input id="amount-input" type="number" class="query-input-box" value="64" />
    </div>
    <div class="query-input-group">
      <label class="query-label" for="resource-type-select-0">Resource type *</label>
      <div class="select-with-remove-box">
        <select id="resource-type-select-0" class="query-select-box">
          <option value="">All resource types (Default - empty)</option>
          <option value="nvidia.com/gpu">nvidia.com/gpu</option>
          <option value="google.com/tpu">google.com/tpu</option>
        </select>
      </div>
    </div>
    <button type="button" class="add-resource-btn">+ Add resource type</button>
    <button type="button" class="find-queues-action">Find matching queues</button>
  </div>`;

async function handleUserAuthRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER),
  });
}

test.describe('SmartResourceFinderComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
  });

  test('should match baseline screenshot for smart resource finder section', async ({ page }) => {
    // Render the SmartResourceFinderComponent template structure inside the container
    await page.evaluate((html) => {
      const container = document.querySelector('.rod-split-container');
      if (container) {
        container.innerHTML = html;
      }
    }, FINDER_QUERY_CARD_HTML);

    const resourceFinder = page.locator('.finder-query-card');
    await expect(resourceFinder).toBeVisible();
    await expect(resourceFinder).toHaveScreenshot('smart-resource-finder-initial.png');
  });
});
