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

export const MOCK_EXPERIMENTS_LIST = {
  experiments: [
    {
      name: 'experiments/10293841',
      title: 'Gemini 1.5 Pro Fine-Tuning - Run 42',
      creator: 'jdoe@google.com',
      priority: 'HIGH',
      createTime: '2026-06-01T10:00:00Z',
      completeTime: '',
      tags: ['gemini', 'jax', 'v5e-64'],
      notes: 'Distributed pre-training & fine-tuning run on TPU v5e-64 pod slice.',
      status: {
        activeState: 'ACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [
          { state: 'RUNNING', count: 8 },
          { state: 'COMPLETED', count: 2 },
          { state: 'FAILED', count: 1 },
          { state: 'SCHEDULING', count: 1 },
        ],
        structuredStatusMessage: {
          userFacingText:
            'Training step 4500/10000 complete (loss: 0.1824). Checkpoint saved to GCS.',
          severity: 'INFO',
          source: 'APPLICATION',
          createTime: '2026-06-01T10:25:00Z',
        },
      },
      resources: [
        { tpuResources: { type: 'v5e', topology: '4x4x4' } },
        { cpuResources: { cpuMillicores: 128000, memoryBytes: 549755813888 } },
      ],
    },
    {
      name: 'experiments/10293842',
      title: 'MMLU & GSM8K Benchmark Evaluation Suite',
      creator: 'alice@google.com',
      priority: 'NORMAL',
      createTime: '2026-06-01T08:00:00Z',
      completeTime: '2026-06-01T09:30:00Z',
      tags: ['eval', 'benchmark', 'mmlu'],
      notes: 'Standardized accuracy evaluation across reasoning benchmarks.',
      status: {
        activeState: 'INACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [{ state: 'COMPLETED', count: 12 }],
        structuredStatusMessage: {
          userFacingText: 'All 12 evaluation work units completed successfully with 0 errors.',
          severity: 'INFO',
          source: 'APPLICATION',
          createTime: '2026-06-01T09:30:00Z',
        },
      },
      resources: [
        { gpuResources: { type: 'nvidia.com/gpu', count: 8 } },
        { cpuResources: { cpuMillicores: 32000, memoryBytes: 137438953472 } },
      ],
    },
    {
      name: 'experiments/10293843',
      title: 'Quantization & Pruning Ablation Study',
      creator: 'bob@google.com',
      priority: 'LOW',
      createTime: '2026-06-01T07:15:00Z',
      completeTime: '2026-06-01T07:22:00Z',
      tags: ['quantization', 'int8', 'ablation'],
      notes: 'INT8 post-training quantization on vision-language model.',
      status: {
        activeState: 'INACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [
          { state: 'FAILED', count: 2 },
          { state: 'STOPPED', count: 2 },
        ],
        structuredStatusMessage: {
          userFacingText: 'OOM killed: Container exceeded memory limit (128Gi) during calibration.',
          severity: 'ERROR',
          source: 'KUBERNETES',
          createTime: '2026-06-01T07:22:00Z',
        },
      },
      resources: [{ cpuResources: { cpuMillicores: 16000, memoryBytes: 137438953472 } }],
    },
    {
      name: 'experiments/10293844',
      title: 'Llama-3 70B Distributed LoRA Fine-Tuning',
      creator: 'charlie@google.com',
      priority: 'HIGH',
      createTime: '2026-06-01T11:00:00Z',
      completeTime: '',
      tags: ['llama-3', 'lora', 'h100'],
      notes: 'Parameter-efficient fine-tuning on high-throughput GPU cluster.',
      status: {
        activeState: 'ACTIVE',
        launchState: 'COMPLETED',
        childStateCounts: [{ state: 'SCHEDULING', count: 4 }],
        structuredStatusMessage: {
          userFacingText:
            'Queue capacity reached; workload queued at position 2 in gpu-cluster-queue.',
          severity: 'WARNING',
          source: 'SCHEDULER',
          createTime: '2026-06-01T11:02:00Z',
        },
      },
      resources: [
        { gpuResources: { type: 'nvidia.com/gpu', count: 32 } },
        { cpuResources: { cpuMillicores: 256000, memoryBytes: 1099511627776 } },
      ],
    },
  ],
  nextPageToken: 'page_token_2',
};

test.describe('Experiments List Page - Screenshot Tests', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 950 });
    await setupMockRoutes(page);

    await page.route(/\/experiments(?:\?.*)?$/, async (route) => {
      if (route.request().resourceType() === 'document') {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(MOCK_EXPERIMENTS_LIST),
      });
    });

    const experimentsResponsePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes('/experiments') &&
        resp.request().resourceType() !== 'document' &&
        resp.status() === 200,
    );

    await page.goto('/experiments');
    await page.waitForSelector('xmc-experiments-list-page');
    await experimentsResponsePromise;

    // Ensure table rows and status badges are rendered
    await page.locator('xmc-experiments-table table tr.mat-mdc-row').first().waitFor({
      state: 'visible',
    });

    await setupScreenshotEnvironment(page);

    // Ensure fixed container height for deterministic cross-environment visual bounding box capture
    await page.addStyleTag({
      content: `
        xmc-experiments-list-page {
          display: block !important;
          box-sizing: border-box !important;
          height: 900px !important;
          min-height: 900px !important;
          overflow: hidden !important;
        }
      `,
    });
  });

  test('should match baseline screenshot for the entire Experiments List page', async ({
    page,
  }) => {
    const listPage = page.locator('xmc-experiments-list-page');
    await expect(listPage).toBeVisible();

    const tableRows = page.locator('xmc-experiments-table table tr.mat-mdc-row:not(.detail-row)');
    await expect(tableRows).toHaveCount(4);

    await expect(listPage).toHaveScreenshot('experiments-list-page.png');
  });
});
