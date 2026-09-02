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

const MOCK_EXPERIMENT = {
  name: 'experiments/10293841',
  title: 'Performance Benchmark Experiment',
  priority: 'HIGH',
  status: {
    launchState: 'COMPLETED',
    activeState: 'ACTIVE',
    createTime: '2026-06-01T12:00:00Z',
    childStateCounts: [
      { state: 'RUNNING', count: 1 },
      { state: 'COMPLETED', count: 1 },
    ],
  },
  tags: ['mlrun', 'benchmark'],
  notes: 'Visual test for experiment page and work units table actions',
  urls: [],
};

const MOCK_WORK_UNITS_RESPONSE = {
  workUnits: [
    {
      name: 'experiments/10293841/workUnits/1',
      priority: 'HIGH',
      workUnitType: 'STANDARD',
      tags: ['worker-0'],
      createTime: '2026-06-01T12:00:00Z',
      completeTime: '',
      currentWorkUnitExecution: {
        status: {
          state: 'RUNNING',
          createTime: '2026-06-01T12:01:00Z',
          messages: [],
        },
        metadata: {
          mlrunUrl:
            'https://console.cloud.google.com/kubernetes/aiml/run/us-central1/test-diagon2?project=xmc-test-project-6',
        },
      },
    },
    {
      name: 'experiments/10293841/workUnits/2',
      priority: 'MEDIUM',
      workUnitType: 'STANDARD',
      tags: ['worker-1'],
      createTime: '2026-06-01T12:00:00Z',
      completeTime: '2026-06-01T12:25:00Z',
      currentWorkUnitExecution: {
        status: {
          state: 'COMPLETED',
          createTime: '2026-06-01T12:01:00Z',
          completeTime: '2026-06-01T12:25:00Z',
          messages: [],
        },
        metadata: {},
      },
    },
  ],
  nextPageToken: '',
};

const MOCK_ARTIFACTS_RESPONSE = {
  artifacts: [],
};

test.describe('Experiment Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 });
    await setupMockRoutes(page);

    // Mock experiment endpoints
    await page.route('**/experiments/10293841', async (route) => {
      if (route.request().resourceType() === 'document') {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_EXPERIMENT),
      });
    });

    await page.route('**/experiments/10293841/workUnits*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_WORK_UNITS_RESPONSE),
      });
    });

    await page.route('**/experiments/10293841/artifacts*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_ARTIFACTS_RESPONSE),
      });
    });

    await page.goto('/experiments/10293841');
    await page.waitForSelector('xmc-experiment-page');
    await page.waitForSelector('xmc-work-units-table table');
    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for the entire Experiment Page', async ({ page }) => {
    const experimentPage = page.locator('xmc-experiment-page');
    await expect(experimentPage).toBeVisible();

    // Verify title is rendered
    await expect(experimentPage.locator('.experiment-title')).toContainText(
      'Performance Benchmark Experiment',
    );

    // Verify Performance header is NOT present
    await expect(experimentPage.locator('th.mat-column-Performance')).not.toBeAttached();

    // Verify Run Profile button is rendered in the first work unit row
    const runProfileBtn = experimentPage.locator(
      'td.mat-column-Urls a[mat-icon-button]:has(mat-icon:has-text("troubleshoot"))',
    );
    await expect(runProfileBtn).toBeVisible();

    // Capture visual golden snapshot of the entire experiment page
    await expect(experimentPage).toHaveScreenshot('experiment-page.png');
  });
});
