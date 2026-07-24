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

const SMART_RESOURCE_FINDER_CSS = `
  .finder-query-card {
    background-color: #ffffff;
    border: 1px solid #dadce0;
    border-radius: 8px;
    padding: 18px 24px;
    display: flex;
    align-items: center;
    gap: 16px;
    flex-wrap: wrap;
    margin-bottom: 24px;
    box-shadow: 0 1px 2px rgba(60, 64, 67, 0.05);
  }
  .query-prefix {
    font-size: 15px;
    font-weight: 500;
    color: #202124;
  }
  .query-input-group {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .query-label {
    font-size: 13px;
    color: #5f6368;
  }
  .query-input-box {
    border: 1px solid #dadce0;
    border-radius: 4px;
    padding: 6px 12px;
    font-size: 14px;
    color: #202124;
    outline: none;
    width: 90px;
    background-color: #ffffff;
  }
  .query-input-box:focus {
    border-color: #1a73e8;
  }
  .select-with-remove-box {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .query-select-box {
    border: 1px solid #dadce0;
    border-radius: 4px;
    padding: 6px 10px;
    font-size: 14px;
    color: #202124;
    outline: none;
    background-color: #ffffff;
  }
  .query-select-box:focus {
    border-color: #1a73e8;
  }
  .add-resource-btn {
    font-size: 13px !important;
    color: #1a73e8 !important;
    font-weight: 500 !important;
    border: none;
    background: none;
    cursor: pointer;
  }
  .find-queues-action {
    margin-left: auto !important;
    font-size: 13px !important;
    background-color: #1a73e8;
    color: #ffffff;
    border: none;
    border-radius: 4px;
    padding: 8px 16px;
    font-weight: 500;
    display: flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
  }
`;

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
    await page.addStyleTag({ content: SMART_RESOURCE_FINDER_CSS });
  });

  test('should match baseline screenshot for smart resource finder section', async ({ page }) => {
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
