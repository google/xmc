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

import type { Page, Route } from '@playwright/test';

export const MOCK_USER = {
  email: 'test@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

/**
 * Generic base layout reset for component screenshot testing.
 */
const BASE_SCREENSHOT_STYLES = `
  html, body {
    width: 100% !important;
    margin: 0 !important;
    padding: 0 !important;
  }
  .screenshot-harness-container {
    width: 100% !important;
    padding: 24px !important;
    box-sizing: border-box !important;
  }
`;

export async function handleUserAuthRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER),
  });
}

export async function handleConfigRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/javascript',
    body: `window.XMC_API_URL = ""; window.XMC_FEATURE_FLAGS = new Proxy({}, { get: () => true });`,
  });
}

export const MOCK_JOBS = [
  {
    xid: '10293841',
    status: 'RUNNING',
    positionInQueue: 0,
    priority: 100,
    requestedResources: [{ resourceType: 'nvidia.com/gpu', count: 8 }],
    workUnitId: 'wu-1',
  },
  {
    xid: '10293842',
    status: 'PENDING',
    positionInQueue: 1,
    priority: 50,
    requestedResources: [{ resourceType: 'nvidia.com/gpu', count: 4 }],
    workUnitId: 'wu-2',
  },
];

export const MOCK_CLUSTER_QUEUES = [
  {
    queueName: 'gpu-cluster-queue',
    cohort: 'gpu-cohort',
    queueCapacity: [
      { resourceType: 'nvidia.com/gpu', freeCapacity: 56, totalCapacity: 64, maxWorkloadSize: 64 },
      { resourceType: 'cpu', freeCapacity: 448, totalCapacity: 512, maxWorkloadSize: 512 },
    ],
  },
  {
    queueName: 'cpu-cluster-queue',
    cohort: 'cpu-cohort',
    queueCapacity: [
      { resourceType: 'cpu', freeCapacity: 448, totalCapacity: 512, maxWorkloadSize: 512 },
    ],
  },
];

export async function handleClusterQueuesRoute(route: Route) {
  const url = route.request().url();
  if (url.includes('/jobs')) {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ jobs: MOCK_JOBS, totalQueueSize: MOCK_JOBS.length }),
    });
  } else if (url.includes('/secondarydata')) {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ queuePredictability: {} }),
    });
  } else {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ queues: MOCK_CLUSTER_QUEUES }),
    });
  }
}

/**
 * Mocks user auth and feature flag configuration routes for Playwright tests.
 */
export async function setupMockRoutes(page: Page) {
  await page.route('/api/user', handleUserAuthRoute);
  await page.route('/config.js', handleConfigRoute);
  await page.route('**/clusterqueues**', handleClusterQueuesRoute);
}

/**
 * Prepares the Playwright page runtime for deterministic component screenshot assertions.
 */
export async function setupScreenshotEnvironment(page: Page) {
  await page.addStyleTag({
    content: BASE_SCREENSHOT_STYLES,
  });
}

/**
 * Mounts a component HTML harness into the DOM for isolated component screenshot testing.
 */
export async function mountComponentHarness(page: Page, htmlContent: string) {
  await page.evaluate((html) => {
    const tempDiv = document.createElement('div');
    tempDiv.innerHTML = html;
    const targetElement = tempDiv.firstElementChild as HTMLElement | null;
    const container =
      document.querySelector('.page-container, .guest-page-container') ||
      document.querySelector('app-root') ||
      document.body;

    if (targetElement) {
      const tagName = targetElement.tagName.toLowerCase();
      const liveElement = document.querySelector(tagName);
      if (liveElement && !targetElement.innerHTML.trim()) {
        targetElement.innerHTML = liveElement.innerHTML;
      }
    }
    container.innerHTML = `<div class="screenshot-harness-container">${tempDiv.innerHTML}</div>`;
  }, htmlContent);
}
