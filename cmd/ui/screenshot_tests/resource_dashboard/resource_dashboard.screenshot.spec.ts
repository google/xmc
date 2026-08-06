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

const MOCK_GPU_QUEUE = {
  queueName: 'gpu-cluster-queue',
  cohortName: 'gpu-cohort',
  queueCapacity: [
    {
      resourceType: 'nvidia.com/gpu',
      totalCapacity: 64,
      freeCapacity: 56,
      maxWorkloadSize: 64,
    },
    {
      resourceType: 'cpu',
      totalCapacity: 512,
      freeCapacity: 448,
      maxWorkloadSize: 512,
    },
  ],
};

const MOCK_CPU_QUEUE = {
  queueName: 'cpu-cluster-queue',
  cohortName: 'cpu-cohort',
  queueCapacity: [
    {
      resourceType: 'cpu',
      totalCapacity: 512,
      freeCapacity: 448,
      maxWorkloadSize: 512,
    },
  ],
};

const MOCK_JOBS = [
  {
    jobName: 'xmc-operation-53296-1',
    xid: '53296',
    user: 'alice@google.com',
    priority: 'HIGH',
    status: 'RUNNING',
    positionInQueue: 1,
    workUnitId: '101',
  },
  {
    jobName: 'xmc-operation-53308-1',
    xid: '53308',
    user: 'bob@google.com',
    priority: 'NORMAL',
    status: 'RUNNING',
    positionInQueue: 2,
    workUnitId: '102',
  },
];

test.describe('Resource Dashboard (ROD) Container - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 2000, height: 900 });
    // Intercept user auth and config API endpoints so authGuard and feature flags succeed
    await setupMockRoutes(page);
    await page.route('**/clusterqueues', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          queues: [MOCK_GPU_QUEUE, MOCK_CPU_QUEUE],
        }),
      });
    });
    await page.route('**/clusterqueues/*/jobs', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          queueName: 'gpu-cluster-queue',
          jobs: MOCK_JOBS,
          totalQueueSize: MOCK_JOBS.length,
        }),
      });
    });
    // Navigate to production route /resources
    await page.goto('/resources');
    await page.waitForSelector('xmc-resource-dashboard');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for initial layout and cluster queues', async ({
    page,
  }) => {
    const dashboard = page.locator('xmc-resource-dashboard');
    await expect(dashboard).toBeVisible();

    // Capture baseline screenshot of full Resource Dashboard container
    await expect(dashboard).toHaveScreenshot('resource-dashboard-initial.png');
  });

  test('should match baseline screenshot after selecting a cluster queue', async ({ page }) => {
    const dashboard = page.locator('xmc-resource-dashboard');
    await expect(dashboard).toBeVisible();

    // Click on resolved cluster queue item
    const queueItem = dashboard.locator('.queue-card', {
      hasText: 'gpu-cluster-queue',
    });
    await queueItem.click();

    // Verify selected class is applied
    await expect(queueItem).toHaveClass(/selected/);

    const tableComponent = dashboard.locator('xmc-queue-jobs-table');
    await expect(tableComponent).toBeVisible();

    // Capture screenshot with selected queue state
    await expect(dashboard).toHaveScreenshot('resource-dashboard-queue-selected.png');
  });
});
