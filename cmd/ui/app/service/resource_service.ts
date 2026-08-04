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

import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';
import { map } from 'rxjs/operators';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
import { ClusterQueue, ClusterQueueListResponse, QueueJobListResponse } from './resource_types';

interface RawQueueCapacity {
  resourceType?: string;
  resource_type?: string;
  totalCapacity?: number;
  total_capacity?: number;
  freeCapacity?: number;
  free_capacity?: number;
  freeCapacityWithBorrowing?: number;
  free_capacity_with_borrowing?: number;
}

interface RawQueueOverview {
  queueName?: string;
  queue_name?: string;
  queueCapacity?: RawQueueCapacity[];
  queue_capacity?: RawQueueCapacity[];
}

interface RawGetFittingQueuesResponse {
  queues?: RawQueueOverview[];
}

/**
 * Angular injectable data service client for fetching Resource Observability Dashboard (ROD)
 * cluster queues and queue jobs.
 */
@Injectable({
  providedIn: 'root',
})
export class ResourceService {
  private readonly http = inject(HttpClient);

  /**
   * Fetches the list of cluster queues.
   */
  getClusterQueues(): Observable<ClusterQueueListResponse> {
    return this.http
      .post<RawGetFittingQueuesResponse>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/queues/fitting`,
        {},
        { withCredentials: true },
      )
      .pipe(
        map((res) => {
          const rawQueues = res?.queues || [];
          const clusterQueues: ClusterQueue[] = rawQueues.map((q) => {
            const name = q.queueName || q.queue_name || 'cluster-queue';
            const rawCapacities = q.queueCapacity || q.queue_capacity || [];
            const flavors = rawCapacities.map((cap) => {
              const resourceType = cap.resourceType || cap.resource_type || 'resource';
              const totalCapacity = cap.totalCapacity ?? cap.total_capacity ?? 0;
              const freeCapacity = cap.freeCapacity ?? cap.free_capacity ?? 0;
              const inUse = Math.max(0, totalCapacity - freeCapacity);
              return {
                name: resourceType,
                resourceName: resourceType,
                nominalQuota: String(totalCapacity),
                inUse: String(inUse),
                reserved: '0',
                freeCapacity: String(freeCapacity),
                largestSlot: String(freeCapacity),
              };
            });

            return {
              name,
              strategy: 'BestEffortFIFO',
              cohort: '',
              status: 'Active',
              admittedWorkloads: 0,
              reservingWorkloads: 0,
              pendingWorkloads: 0,
              pendingJobsCount: 0,
              admittedJobsCount: 0,
              localQueues: [],
              flavors,
            };
          });
          return { clusterQueues };
        }),
      );
  }

  /**
   * Fetches the list of pending and running jobs for a given queue.
   */
  getQueueJobs(queueName?: string): Observable<QueueJobListResponse> {
    const targetQueueName = queueName ?? '';
    return of({
      queueName: targetQueueName,
      jobs: [],
    });
  }
}
