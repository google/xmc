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
  email: 'admin@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

export const MOCK_USER_INFO = {
  userEmail: 'test@google.com',
  privileges: ['PRIVILEGE_ACCESS_ADMIN_PANEL'],
};

/**
 * Generic base layout reset for page screenshot testing.
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
  [data-screenshot-target],
  [class*="placeholder"],
  [class*="scaffolding"] {
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

export async function handleUserInfoRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_USER_INFO),
  });
}

export async function handleConfigRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/javascript',
    body: `window.XMC_API_URL = ""; window.XMC_FEATURE_FLAGS = new Proxy({}, { get: () => true });`,
  });
}

export const MOCK_CLUSTER_QUEUES = {
  queues: [
    {
      queueName: 'gpu-cluster-queue',
      queueCapacity: [{ resourceType: 'nvidia.com/gpu', totalCapacity: 64, freeCapacity: 56 }],
    },
    {
      queueName: 'cpu-cluster-queue',
      queueCapacity: [{ resourceType: 'cpu', totalCapacity: 512, freeCapacity: 448 }],
    },
  ],
};

export const MOCK_JOBS = [
  {
    xid: 10293841,
    workUnitId: 1,
    status: 'RUNNING',
    priority: 100,
    requestedResources: [{ resourceType: 'nvidia.com/gpu', count: 8 }],
    user: 'jdoe@google.com',
  },
  {
    xid: 10293842,
    workUnitId: 2,
    status: 'PENDING',
    priority: 50,
    requestedResources: [{ resourceType: 'nvidia.com/gpu', count: 4 }],
    user: 'jdoe@google.com',
  },
];

export async function handleClusterQueuesRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(MOCK_CLUSTER_QUEUES),
  });
}

export async function handleQueueJobsRoute(route: Route) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ jobs: MOCK_JOBS, totalQueueSize: MOCK_JOBS.length }),
  });
}

export async function handleSecondaryDataRoute(route: Route) {
  const mockPredictabilityData = {
    'gpu-cluster-queue': {
      queuePredictability: [
        {
          velocityRange: 'RANGE_PRESET_LAST_HOUR',
          jobVelocity: 12.5,
          resourceVelocity: {
            resourceType: 'nvidia.com/gpu',
            velocity: 100,
          },
          pendingJobs: 3,
          pendingResourceRequests: {
            resourceType: 'nvidia.com/gpu',
            count: 24,
          },
        },
      ],
    },
    'cpu-cluster-queue': {
      queuePredictability: [
        {
          velocityRange: 'RANGE_PRESET_LAST_HOUR',
          jobVelocity: 5.0,
          resourceVelocity: {
            resourceType: 'cpu',
            velocity: 40,
          },
          pendingJobs: 2,
          pendingResourceRequests: {
            resourceType: 'cpu',
            count: 16,
          },
        },
      ],
    },
  };

  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      queuePredictability: mockPredictabilityData,
    }),
  });
}

/**
 * UI test harness utilities for Playwright visual screenshot testing.
 * Mocks user auth, feature flag configuration, and cluster queue data routes.
 */
export async function setupMockRoutes(page: Page) {
  await page.route(/\/api\/user/, handleUserAuthRoute);
  await page.route(/\/health\/userinfo/, handleUserInfoRoute);
  await page.route(/\/config\.js/, handleConfigRoute);
  await page.route(/\/clusterqueues$/, handleClusterQueuesRoute);
  await page.route(/\/clusterqueues\/.*\/jobs/, handleQueueJobsRoute);
  await page.route(/.*batchGetSecondaryData.*/, handleSecondaryDataRoute);
}

/**
 * Prepares the Playwright page runtime for deterministic component screenshot assertions.
 */
export async function setupScreenshotEnvironment(page: Page) {
  await page.addStyleTag({
    content: BASE_SCREENSHOT_STYLES,
  });
}
