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
  Artifact,
  Experiment,
  StatusMessage,
  WorkUnit,
} from 'app/protos/experiment_state_server';

export const TOTAL_STATUS_MESSAGES: StatusMessage[] = [];
export const MOCK_HISTORICAL_STATUS_MESSAGES: StatusMessage[] = [
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

export const EXPERIMENT_1: Experiment = {
  name: 'experiments/1',
  title: 'Experiment 1',
  notes: 'Notes for experiment 1',
  tags: ['tag1', 'tag2'],
  priority: 'LOW',
  createTime: '2026-06-08T00:00:00Z',
};

export const EXPERIMENT_2: Experiment = {
  name: 'experiments/2',
  title: 'Experiment 2',
  notes: 'Notes for experiment 2',
  tags: ['tag3'],
  priority: 'NORMAL',
  createTime: '2026-06-08T01:00:00Z',
};

export const EXPERIMENT_LIST: Experiment[] = [EXPERIMENT_1, EXPERIMENT_2];

export const WORK_UNIT_1: WorkUnit = {
  name: 'experiments/1/workUnits/1',
  createTime: '2026-06-08T00:00:00Z',
  currentWorkUnitExecution: {
    metadata: {
      managementCluster: {
        project: 'test-project',
        location: 'us-central1',
        clusterName: 'test-cluster',
      },
      executingCluster: {
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

export const WORK_UNIT_2: WorkUnit = {
  name: 'experiments/1/workUnits/2',
  // Missing metadata for testing undefined URL resolution
};

export const WORK_UNIT_3: WorkUnit = {
  name: 'experiments/1/workUnits/3',
};

export const WORK_UNIT_4: WorkUnit = {
  name: 'experiments/1/workUnits/4',
};

export const WORK_UNIT_LIST: WorkUnit[] = [WORK_UNIT_1, WORK_UNIT_2, WORK_UNIT_3, WORK_UNIT_4];

export const ARTIFACT_LIST: Artifact[] = [];
export const WORK_UNIT_1_ARTIFACT_LIST: Artifact[] = [];
