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

const MOCK_HEALTH_STATUS = {
  healthState: 'HEALTH_STATE_UNHEALTHY',
  systemInfo: {
    appVersion: '1.5.1',
    buildVersion: 'v1.5.1-build.42',
    essDbSchemaVersion: '3',
    xidDbSchemaVersion: '1',
    observabilityUrl: 'https://monitoring.xmc.internal',
    isSchedulingPaused: false,
  },
  components: [
    {
      name: 'actuator-deployment',
      type: 'COMPONENT_TYPE_CORE_SERVICE',
      healthState: 'HEALTH_STATE_HEALTHY',
    },
    {
      name: 'experiment-state-server-deployment',
      type: 'COMPONENT_TYPE_CORE_SERVICE',
      healthState: 'HEALTH_STATE_UNHEALTHY',
      errorMessage:
        'Error: Connection timeout to Spanner experiment-db after 5000ms. Retrying in 30s.',
    },
    {
      name: 'xid-service-deployment',
      type: 'COMPONENT_TYPE_CORE_SERVICE',
      healthState: 'HEALTH_STATE_HEALTHY',
    },
    {
      name: 'Cloud Spanner (experiment-db)',
      type: 'COMPONENT_TYPE_EXTERNAL_DEPENDENCY',
      healthState: 'HEALTH_STATE_HEALTHY',
    },
    {
      name: 'Cloud Tasks',
      type: 'COMPONENT_TYPE_EXTERNAL_DEPENDENCY',
      healthState: 'HEALTH_STATE_HEALTHY',
    },
    {
      name: 'Internal Git Provider',
      type: 'COMPONENT_TYPE_CUSTOM_COMPONENT',
      healthState: 'HEALTH_STATE_HEALTHY',
    },
  ],
  healthCheckTime: { seconds: 1717804800, nanos: 0 },
};

const MOCK_CONFIGS = {
  configGroups: [
    {
      displayName: 'Main Configuration',
      configs: [
        { key: 'spannerUrl', value: 'spanner.googleapis.com/projects/xmanager-prod' },
        { key: 'maxConcurrentWorkloads', value: '500' },
      ],
    },
  ],
};

const MOCK_USER_INFO = {
  userEmail: 'test@google.com',
  privileges: ['PRIVILEGE_ACCESS_ADMIN_PANEL'],
};

test.describe('Admin Panel Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 });

    // Mock generic auth/config
    await setupMockRoutes(page);

    // Mock Admin Panel specific endpoints
    await page.route('**/health/status', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_HEALTH_STATUS),
      });
    });

    await page.route('**/health/configs', async (route) => {
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

    // Go to admin panel
    await page.goto('/admin');
    await page.waitForSelector('xmc-admin-panel');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for the entire Admin Panel page', async ({ page }) => {
    // Take screenshot of the entire admin panel page viewport
    await expect(page).toHaveScreenshot('admin-panel-page.png');
  });
});
