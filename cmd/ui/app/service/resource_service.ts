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

import {
  GetFittingQueuesResponse,
  QueueOverview,
  QueueResourceCapacity,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
import { ClusterQueue, ClusterQueueListResponse, QueueJobListResponse } from './resource_types';

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
      .get<GetFittingQueuesResponse>(`${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues`, {
        withCredentials: true,
      })
      .pipe(
        map((res) => {
          const rawQueues: QueueOverview[] = res?.queues || [];
          const clusterQueues: ClusterQueue[] = rawQueues.map((q) => {
            const name = q.queueName || 'cluster-queue';
            const rawCapacities: QueueResourceCapacity[] = q.queueCapacity || [];
            const flavors = rawCapacities.map((cap) => {
              const resourceType = cap.resourceType || 'resource';
              const totalCapacity = cap.totalCapacity ?? 0;
              const freeCapacity = cap.freeCapacity ?? 0;
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
