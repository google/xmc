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

import type { WorkUnitStatus } from './experiment_state_server';

export type WorkUnitState = WorkUnitStatus.WorkUnitState;

export type RangePreset =
  | 'RANGE_PRESET_UNSPECIFIED'
  | 'RANGE_PRESET_LAST_HOUR'
  | 'RANGE_PRESET_LAST_DAY'
  | 'RANGE_PRESET_LAST_WEEK'
  | number;

export interface Resource {
  resourceType?: string;
  count?: number;
}

export interface ResourceVelocity {
  resourceType?: string;
  velocity?: number;
}

export interface QueueResourceCapacity {
  resourceType?: string;
  totalCapacity?: number;
  freeCapacity?: number;
  freeCapacityWithBorrowing?: number;
}

export interface QueueOverview {
  queueName?: string;
  queueCapacity?: QueueResourceCapacity[];
}

export interface QueuePredictability {
  pendingResourceRequests?: Resource;
  resourceVelocity?: ResourceVelocity;
  pendingJobs?: number;
  jobVelocity?: number;
  velocityRange?: RangePreset;
}

export interface ClusterQueueSecondaryMetrics {
  queuePredictability?: QueuePredictability[];
}

export interface ListClusterQueuesRequest {
  [key: string]: any;
}

export interface ListClusterQueuesResponse {
  queues?: QueueOverview[];
}

export interface BatchGetQueueSecondaryDataRequest {
  queueNames?: string[];
  range?: RangePreset;
}

export interface BatchGetQueueSecondaryDataResponse {
  queuePredictability?: { [key: string]: ClusterQueueSecondaryMetrics };
}

export interface DashboardJob {
  xid?: number;
  workUnitId?: number;
  user?: string;
  priority?: number;
  status?: WorkUnitState;
  timeInQueueSeconds?: number;
  requestedResources?: Resource[];
  positionInQueue?: number;
  createTime?: string | { seconds?: number | string; nanos?: number };
  tags?: string[];
  experimentTitle?: string;
}

export interface GetRunningAndPendingJobsRequest {
  queueName?: string;
  pageSize?: number;
  pageToken?: string;
  orderBy?: string;
}

export interface GetRunningAndPendingJobsResponse {
  jobs?: DashboardJob[];
  nextPageToken?: string;
  totalQueueSize?: number;
}

export interface GetSubmitQueueRequest {
  queueName?: string;
}

export interface GetSubmitQueueResponse {
  submitQueue?: string;
}
