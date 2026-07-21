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

/** Reference to a connected LocalQueue. */
export interface LocalQueueRef {
  name: string;
  namespace: string;
}

/** Primary resource metrics for a queue flavor. */
export interface ResourceFlavorMetric {
  name: string;
  resourceName: string;
  nominalQuota: string;
  inUse: string;
  reserved: string;
  freeCapacity: string;
  largestSlot: string;
}

/** Primary data model for a ClusterQueue. */
export interface ClusterQueue {
  name: string;
  strategy: string;
  cohort: string;
  status: string;
  statusMessage?: string;
  lastTransitionTime?: string;
  admittedWorkloads: number;
  reservingWorkloads: number;
  pendingWorkloads: number;
  pendingJobsCount?: number;
  requestedResources?: string;
  dominantResource?: string;
  dominantResourceUnits?: number;
  admittedJobsCount?: number;
  resourceVelocity?: string;
  jobVelocity?: string;
  localQueues: LocalQueueRef[];
  flavors: ResourceFlavorMetric[];
}

/** Response payload for listing ClusterQueues. */
export interface ClusterQueueListResponse {
  clusterQueues: ClusterQueue[];
}

/** Details for a pending or running job in a queue. */
export interface QueueJobInfo {
  position: string;
  xid: string;
  workUnitId: string;
  jobName: string;
  user: string;
  resources: string;
  priority: string;
  status: string;
  timeInQueue: string;
  queueName: string;
  creationTime?: string;
}

/** Response payload for queue jobs endpoint. */
export interface QueueJobListResponse {
  queueName: string;
  jobs: QueueJobInfo[];
}
