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

export const TOTAL_STATUS_MESSAGES: any[] = [];
export const MOCK_HISTORICAL_STATUS_MESSAGES: any[] = [
  {
    name: 'experiments/1/statusMessages/1',
    createTime: '2026-06-08T00:00:00Z',
    content: {
      userFacingText: 'Application is being initialized.',
      severity: 'INFO',
      source: 'APPLICATION',
    },
  },
  {
    name: 'experiments/1/statusMessages/2',
    createTime: '2026-06-08T00:01:00Z',
    content: {
      userFacingText: 'Critical failure: connection lost.',
      severity: 'ERROR',
      source: 'APPLICATION',
    },
  },
];

export const EXPERIMENT_1: any = {
  name: 'experiments/1',
  title: 'Experiment 1',
  notes: 'Notes for experiment 1',
  tags: ['tag1', 'tag2'],
  priority: 'LOW',
  createTime: '2026-06-08T00:00:00Z',
};

export const EXPERIMENT_2: any = {
  name: 'experiments/2',
  title: 'Experiment 2',
  notes: 'Notes for experiment 2',
  tags: ['tag3'],
  priority: 'NORMAL',
  createTime: '2026-06-08T01:00:00Z',
};

export const EXPERIMENT_LIST: any[] = [EXPERIMENT_1, EXPERIMENT_2];

export const WORK_UNIT_1: any = {
  name: 'experiments/1/workUnits/1',
  createTime: '2026-06-08T00:00:00Z',
  currentWorkUnitExecution: {
    metadata: {
      managementCluster: {
        project: 'test-project',
        location: 'us-central1',
        clusterName: 'test-cluster',
      },
      resourceName: 'test-resource',
      namespace: 'test-namespace',
      serviceAccount: 'test-sa',
    },
  },
};

export const WORK_UNIT_2: any = {
  name: 'experiments/1/workUnits/2',
  // Missing metadata for testing undefined URL resolution
};

export const WORK_UNIT_3: any = {
  name: 'experiments/1/workUnits/3',
};

export const WORK_UNIT_4: any = {
  name: 'experiments/1/workUnits/4',
};

export const WORK_UNIT_LIST: any[] = [WORK_UNIT_1, WORK_UNIT_2, WORK_UNIT_3, WORK_UNIT_4];

export const ARTIFACT_LIST: any[] = [];
export const WORK_UNIT_1_ARTIFACT_LIST: any[] = [];

export const MOCK_CLUSTER_QUEUE_1: any = {
  name: 'cpu-cluster-queue',
  strategy: 'BestEffortFIFO',
  cohort: 'None',
  status: 'Active',
  statusMessage: 'Can admit new workloads',
  lastTransitionTime: '2026-06-18T10:49:48Z',
  admittedWorkloads: 0,
  reservingWorkloads: 0,
  pendingWorkloads: 0,
  localQueues: [{ name: 'cpu-queue', namespace: 'cpu-queue' }],
  flavors: [
    {
      name: 'cpu-flavor',
      resourceName: 'cpu',
      nominalQuota: '100M',
      inUse: '0',
      reserved: '0',
      freeCapacity: '100M',
      largestSlot: '100M -> 100M',
    },
  ],
};

export const MOCK_CLUSTER_QUEUES: any[] = [MOCK_CLUSTER_QUEUE_1];
