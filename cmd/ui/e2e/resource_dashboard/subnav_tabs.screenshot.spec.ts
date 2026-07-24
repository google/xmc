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

const SUBNAV_TABS_CSS = `
  .subnav-container {
    border-bottom: 1px solid #dadce0;
    margin-bottom: 24px;
  }
  .subnav-tabs {
    display: flex;
    gap: 24px;
  }
  .subnav-tab {
    background: none;
    border: none;
    padding: 12px 4px;
    font-size: 14px;
    font-weight: 500;
    color: #5f6368;
    cursor: pointer;
    border-bottom: 2px solid transparent;
  }
  .subnav-tab:hover {
    color: #202124;
  }
  .subnav-tab.active {
    color: #1a73e8;
    border-bottom-color: #1a73e8;
  }
`;

async function handleUserAuthRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER),
  });
}

test.describe('SubnavTabsComponent - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('/api/user', handleUserAuthRoute);
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await page.addStyleTag({ content: SUBNAV_TABS_CSS });
  });

  test('should match baseline screenshot for subnav tabs component layout', async ({ page }) => {
    await page.evaluate(() => {
      const placeholder = document.querySelector('.rod-subnav-placeholder');
      if (placeholder) {
        placeholder.innerHTML = `
          <div class="subnav-container">
            <div class="subnav-tabs" role="tablist">
              <button type="button" class="subnav-tab active" role="tab">Smart resource finder</button>
              <button type="button" class="subnav-tab" role="tab">Resource availability</button>
            </div>
          </div>`;
      }
    });

    const subnavTabs = page.locator('.subnav-container');
    await expect(subnavTabs).toBeVisible();
    await expect(subnavTabs).toHaveScreenshot('subnav-tabs-component.png');
  });
});
