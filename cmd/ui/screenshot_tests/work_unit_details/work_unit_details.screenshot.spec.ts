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

export const MOCK_EXPERIMENT = {
  name: 'experiments/10293841',
  title: 'Gemini 1.5 Pro Fine-Tuning - Run 42',
  creator: 'jdoe@google.com',
  priority: 'HIGH',
  createTime: '2026-06-01T10:00:00Z',
  completeTime: '',
  tags: ['gemini', 'jax', 'v5e-64'],
  notes: 'Large-scale distributed fine-tuning run on TPU v5e pod slice.',
  status: {
    activeState: 'ACTIVE',
    launchState: 'COMPLETED',
  },
};

export const MOCK_WORK_UNIT_DETAILS = {
  name: 'experiments/10293841/workUnits/1',
  workUnitType: 'STANDARD',
  priority: 'HIGH',
  createTime: '2026-06-01T10:00:00Z',
  updateTime: '2026-06-01T10:25:00Z',
  tags: ['coordinator', 'rank-0', 'tpu-v5e-slice-0'],
  notes:
    'Primary coordinator work unit for multi-host gradient synchronization and checkpoint saving.',
  urls: [
    {
      url: 'https://console.cloud.google.com/kubernetes/pod/us-central1/tpu-cluster/xmc/coordinator-0',
      displayName: 'Pod Logs',
      icon: 'description',
      priority: 1,
    },
    {
      url: 'https://tensorboard.googleplex.com/experiment/10293841',
      displayName: 'Tensorboard',
      icon: 'insights',
      priority: 2,
    },
    {
      url: 'https://monitoring.xmc.internal/dashboards/pod-diagnostics',
      displayName: 'Pod Diagnostics',
      icon: 'analytics',
      priority: 3,
    },
  ],
  currentWorkUnitExecution: {
    createTime: '2026-06-01T10:01:00Z',
    metadata: {
      managementCluster: {
        project: 'xmanager-prod',
        location: 'us-central1',
        clusterName: 'xmc-gke-prod-01',
      },
      resourceName: 'gemini-run42-coordinator',
      namespace: 'xmc-workloads',
      serviceAccount: 'xmc-workload-sa@xmanager-prod.iam.gserviceaccount.com',
    },
    status: {
      state: 'RUNNING',
      updateTime: '2026-06-01T10:25:00Z',
      userMessage: {
        userFacingText:
          'Training step 4500/10000 complete (loss: 0.1824). Gradient synchronization latency: 1.2ms.',
        severity: 'INFO',
        source: 'APPLICATION',
        createTime: '2026-06-01T10:25:00Z',
      },
      systemMessage: {
        userFacingText: 'Kueue assigned resource slice on tpu-cluster-queue',
        severity: 'INFO',
        source: 'SCHEDULER',
        createTime: '2026-06-01T10:01:00Z',
      },
      detailedStatus: {
        jobsetStatus: {
          replicatedJobsStatus: [
            {
              name: 'coordinator-job',
              active: 1,
              ready: 1,
              succeeded: 0,
              failed: 0,
            },
            {
              name: 'worker-job',
              active: 8,
              ready: 8,
              succeeded: 0,
              failed: 0,
            },
          ],
        },
      },
    },
  },
};

export const MOCK_WORK_UNIT_ARTIFACTS = {
  artifacts: [
    {
      name: 'artifacts/201',
      parent: 'experiments/10293841/workUnits/1',
      lifecyclePhase: 'INPUT',
      tags: ['init-weights'],
      notes: 'Initial rank-0 partition weights.',
      payload: {
        url: {
          url: 'gs://xmc-checkpoints/gemini-1.5-pro-run42/shard-00000-of-00008.bin',
          displayName: 'Shard 0 Weights',
          icon: 'inventory_2',
        },
      },
    },
    {
      name: 'artifacts/202',
      parent: 'experiments/10293841/workUnits/1',
      lifecyclePhase: 'EXECUTION',
      tags: ['profile', 'xprof'],
      notes: 'TensorFlow / JAX trace profile.',
      payload: {
        url: {
          url: 'https://xprof.corp.google.com/trace/10293841-wu1',
          displayName: 'JAX Profile Trace',
          icon: 'troubleshoot',
        },
      },
    },
    {
      name: 'artifacts/203',
      parent: 'experiments/10293841/workUnits/1',
      lifecyclePhase: 'OUTPUT',
      tags: ['checkpoint', 'rank-0'],
      notes: 'Step 4500 checkpoint shard.',
      payload: {
        url: {
          url: 'gs://xmc-checkpoints/gemini-1.5-pro-run42/ckpt-4500-shard-0.bin',
          displayName: 'Checkpoint 4500 (Shard 0)',
          icon: 'download',
        },
      },
    },
  ],
};

export const MOCK_WORK_UNIT_STATUS_MESSAGES = {
  statusMessages: [
    {
      name: 'experiments/10293841/workUnits/1/statusMessages/1',
      createTime: '2026-06-01T10:00:10Z',
      content: {
        userFacingText: 'Work unit submitted to Kubernetes JobSet controller.',
        severity: 'INFO',
        source: 'KUBERNETES',
        createTime: '2026-06-01T10:00:10Z',
      },
    },
    {
      name: 'experiments/10293841/workUnits/1/statusMessages/2',
      createTime: '2026-06-01T10:01:00Z',
      content: {
        userFacingText: 'All 9 pod replicas (1 coordinator + 8 workers) scheduled and running.',
        severity: 'INFO',
        source: 'SCHEDULER',
        createTime: '2026-06-01T10:01:00Z',
      },
    },
    {
      name: 'experiments/10293841/workUnits/1/statusMessages/3',
      createTime: '2026-06-01T10:25:00Z',
      content: {
        userFacingText:
          'Training step 4500/10000 complete (loss: 0.1824). Gradient synchronization latency: 1.2ms.',
        severity: 'INFO',
        source: 'APPLICATION',
        createTime: '2026-06-01T10:25:00Z',
      },
    },
  ],
};

test.describe('WorkUnit Details Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 1000 });
    await setupMockRoutes(page);

    await page.route(/\/experiments\/10293841$/, async (route) => {
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

    await page.route(/\/experiments\/10293841\/workUnits\/1$/, async (route) => {
      if (route.request().resourceType() === 'document') {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_WORK_UNIT_DETAILS),
      });
    });

    await page.route(
      /\/experiments\/10293841\/workUnits\/1\/artifacts:search(?:\?.*)?$/,
      async (route) => {
        if (route.request().resourceType() === 'document') {
          await route.fallback();
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(MOCK_WORK_UNIT_ARTIFACTS),
        });
      },
    );

    await page.route(
      /\/experiments\/10293841\/workUnits\/1\/statusMessages(?:\?.*)?$/,
      async (route) => {
        if (route.request().resourceType() === 'document') {
          await route.fallback();
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(MOCK_WORK_UNIT_STATUS_MESSAGES),
        });
      },
    );

    const expResponsePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes('/experiments/10293841') &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );
    const workUnitResponsePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes('/workUnits/1') &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/experiments/10293841/workUnits/1');
    await page.waitForSelector('xmc-work-unit-page');
    await expResponsePromise;
    await workUnitResponsePromise;

    // Wait for header and jobs table to render
    await page.locator('xmc-work-unit-header').waitFor({ state: 'visible' });
    await page.locator('xmc-work-unit-jobs-table table tr.mat-mdc-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);
  });

  test('should match baseline screenshot for the WorkUnit Details page (Jobs tab)', async ({
    page,
  }) => {
    const wuPage = page.locator('xmc-work-unit-page');
    await expect(wuPage).toBeVisible();

    const title = page.locator('.work-unit-title-container h1');
    await expect(title).toHaveText('Work Unit 1');

    const jobRows = page.locator('xmc-work-unit-jobs-table table tr.mat-mdc-row');
    await expect(jobRows).toHaveCount(2);

    await expect(wuPage).toHaveScreenshot('work-unit-details-page.png');
  });

  test('should match baseline screenshot for the WorkUnit Artifacts tab', async ({ page }) => {
    const wuPage = page.locator('xmc-work-unit-page');
    await expect(wuPage).toBeVisible();

    // Switch to Artifacts tab
    const artifactsTab = page.getByRole('tab', { name: 'Artifacts' });
    await artifactsTab.click();

    await page.locator('xmc-artifacts-list .pipeline-container').waitFor({ state: 'visible' });
    await page
      .locator('xmc-artifacts-list xmc-artifact-card')
      .first()
      .waitFor({ state: 'visible' });

    await expect(wuPage).toHaveScreenshot('work-unit-details-artifacts-tab.png');
  });
});
