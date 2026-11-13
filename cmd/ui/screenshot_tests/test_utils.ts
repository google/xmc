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

import {
  MOCK_EXPERIMENT_ARTIFACTS,
  MOCK_EXPERIMENT_DETAILS,
  MOCK_EXPERIMENT_STATUS_MESSAGES,
  MOCK_EXPERIMENT_WORK_UNITS,
  MOCK_EXPERIMENTS_LIST,
} from './mock_experiment_fixtures';
import {
  MOCK_CLUSTER_QUEUES,
  MOCK_CONFIGS,
  MOCK_DASHBOARDS_LIST,
  MOCK_HEALTH_STATUS,
  MOCK_JOBS,
  MOCK_PREDICTABILITY_DATA,
  MOCK_USER,
  MOCK_USER_INFO,
} from './mock_platform_fixtures';
import {
  MOCK_WORK_UNIT_ARTIFACTS,
  MOCK_WORK_UNIT_DETAILS,
  MOCK_WORK_UNIT_STATUS_MESSAGES,
} from './mock_work_unit_fixtures';

/**
 * Generic base layout reset for page screenshot testing.
 */
const BASE_SCREENSHOT_STYLES = `
  html, body {
    width: 100% !important;
    margin: 0 !important;
    padding: 0 !important;
  }
  .sidenav-container .mat-drawer-content,
  .sidenav-container .mat-sidenav-content {
    margin-left: 200px !important;
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

function createJsonRouteHandler(payload: unknown) {
  return async (route: Route) => {
    if (route.request().resourceType() === 'document') {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(payload),
    });
  };
}

const MOCK_JSON_ROUTES: Array<[RegExp, unknown]> = [
  [/\/api\/user/, MOCK_USER],
  [/\/health\/userinfo/, MOCK_USER_INFO],
  [/\/health\/status/, MOCK_HEALTH_STATUS],
  [/\/health\/configs/, MOCK_CONFIGS],
  [/\/health\/scheduling$/, { isSchedulingPaused: false }],
  [/\/clusterqueues(?:\?.*)?$/, MOCK_CLUSTER_QUEUES],
  [/\/clusterqueues\/.*\/jobs/, { jobs: MOCK_JOBS, totalQueueSize: MOCK_JOBS.length }],
  [/.*batchGetSecondaryData.*/, { queuePredictability: MOCK_PREDICTABILITY_DATA }],
  [/\/dashboards(?:\?.*)?$/, MOCK_DASHBOARDS_LIST],
  [/\/experiments(?:\?.*)?$/, MOCK_EXPERIMENTS_LIST],
  [/\/experiments\/\d+(?:\?.*)?$/, MOCK_EXPERIMENT_DETAILS],
  [/\/experiments\/\d+\/workUnits(?:\?.*)?$/, MOCK_EXPERIMENT_WORK_UNITS],
  [/\/experiments\/\d+\/artifacts:search(?:\?.*)?$/, MOCK_EXPERIMENT_ARTIFACTS],
  [/\/experiments\/\d+\/statusMessages(?:\?.*)?$/, MOCK_EXPERIMENT_STATUS_MESSAGES],
  [/\/experiments\/\d+\/workUnits\/\d+(?:\?.*)?$/, MOCK_WORK_UNIT_DETAILS],
  [/\/experiments\/\d+\/workUnits\/\d+\/artifacts:search(?:\?.*)?$/, MOCK_WORK_UNIT_ARTIFACTS],
  [/\/experiments\/\d+\/workUnits\/\d+\/statusMessages(?:\?.*)?$/, MOCK_WORK_UNIT_STATUS_MESSAGES],
];

/**
 * UI test harness utilities for Playwright visual screenshot testing.
 * Mocks user auth, feature flag configuration, admin panel, cluster queue data,
 * experiments, work units, artifacts, and status messages routes.
 */
export async function setupMockRoutes(page: Page) {
  await page.route(/\/config\.js/, async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: `window.XMC_API_URL = ""; window.XMC_FEATURE_FLAGS = new Proxy({}, { get: () => true });`,
    });
  });

  for (const [pattern, payload] of MOCK_JSON_ROUTES) {
    await page.route(pattern, createJsonRouteHandler(payload));
  }
}

/**
 * Prepares the Playwright page runtime for deterministic component screenshot assertions.
 */
export async function setupScreenshotEnvironment(page: Page) {
  await page.addStyleTag({
    content: BASE_SCREENSHOT_STYLES,
  });
}
