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

import type {
  HealthStatusResponse,
  ListConfigEntriesResponse,
  UserInfoResponse,
} from 'app/protos/health_service';
import type {
  ClusterQueueSecondaryMetrics,
  DashboardJob,
  ListClusterQueuesResponse,
} from 'app/protos/rod';
import type { ListDashboardsResponse } from 'app/service/dashboard_models';
import { createMockDashboardList } from 'app/testing/mock_dashboards';

import type { UserResponse } from '../app/service/auth.service';

export const MOCK_USER: UserResponse = {
  email: 'admin@google.com',
  name: 'Test User',
  pictureUrl: '',
  sessionExpiresAt: 9999999999,
  allowedDomains: ['google.com'],
};

export const MOCK_USER_INFO: UserInfoResponse = {
  userEmail: 'admin@google.com',
  privileges: ['PRIVILEGE_ACCESS_ADMIN_PANEL'],
};

export const MOCK_HEALTH_STATUS: HealthStatusResponse = {
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
      stability: 'STABLE',
    },
    {
      name: 'Metrics Collector',
      type: 'COMPONENT_TYPE_CUSTOM_COMPONENT',
      healthState: 'HEALTH_STATE_HEALTHY',
      stability: 'PREVIEW',
    },
    {
      name: 'Custom GPU Profiler',
      type: 'COMPONENT_TYPE_CUSTOM_COMPONENT',
      healthState: 'HEALTH_STATE_HEALTHY',
      stability: 'EXPERIMENTAL',
    },
  ],
  healthCheckTime: { seconds: 1717804800, nanos: 0 },
};

export const MOCK_CONFIGS: ListConfigEntriesResponse = {
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

export const MOCK_CLUSTER_QUEUES: ListClusterQueuesResponse = {
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

export const MOCK_JOBS: DashboardJob[] = [
  {
    xid: 10293841,
    workUnitId: 1,
    experimentTitle: 'llama-finetune-sweep',
    status: 'RUNNING',
    runningDurationSeconds: 51720,
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

export const MOCK_PREDICTABILITY_DATA: Record<string, ClusterQueueSecondaryMetrics> = {
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

export const MOCK_DASHBOARDS_LIST: ListDashboardsResponse = {
  dashboards: createMockDashboardList(),
};
