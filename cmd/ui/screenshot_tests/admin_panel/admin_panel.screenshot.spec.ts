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

const MOCK_CONFIGS = {
  configs: [
    {
      key: 'FEATURE_NEW_ROD_UI',
      value: 'true',
      type: 'BOOL',
      description: 'Enable the redesigned Resource Dashboard UI.',
      modifiedBy: 'jdoe@google.com',
      modifiedAt: '2026-03-30T10:15:00Z',
    },
    {
      key: 'MAX_PENDING_JOBS_PER_QUEUE',
      value: '500',
      type: 'INT',
      description: 'Maximum pending jobs allowed in a single queue before throttling.',
      modifiedBy: 'admin@google.com',
      modifiedAt: '2026-03-28T14:22:10Z',
    },
    {
      key: 'DEFAULT_SCHEDULER_MODE',
      value: 'FAIR_SHARE',
      type: 'STRING',
      description: 'Default scheduling algorithm for cluster resource allocation.',
      modifiedBy: 'sys-admin@google.com',
      modifiedAt: '2026-03-25T08:45:30Z',
    },
  ],
};

const MOCK_USER_INFO = {
  user: {
    email: 'admin@google.com',
    roles: ['admin', 'operator'],
  },
  systemStatus: {
    version: 'v1.5.1-build.42',
    uptimeSeconds: 864200,
    healthy: true,
    activeNodes: 128,
  },
};

test.describe('Admin Panel Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1240, height: 1300 });
    await setupMockRoutes(page);

    await page.route('**/admin/configs', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_CONFIGS),
      });
    });

    await page.route('**/health/userinfo', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_USER_INFO),
      });
    });

    await page.goto('/admin');
    await page.waitForSelector('xmc-admin-panel');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for the entire Admin Panel page', async ({ page }) => {
    // Take screenshot of the entire admin panel page viewport
    await expect(page).toHaveScreenshot('admin-panel-page.png');
  });
});
