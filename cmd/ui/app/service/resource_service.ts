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

import { ClusterQueueListResponse, QueueJobListResponse } from './resource_types';

/**
 * Service for interacting with ClusterQueues and Resource Observability data.
 */
@Injectable({
  providedIn: 'root',
})
export class ResourceService {
  private readonly http = inject(HttpClient);

  /**
   * Fetches the list of ClusterQueues with primary data metrics.
   */
  getClusterQueues(_period?: string): Observable<ClusterQueueListResponse> {
    // TODO(#186): Switch to real backend endpoint when rod-data-provider is deployed.
    return of({
      clusterQueues: [
        {
          name: 'cpu-cluster-queue',
          strategy: 'BestEffortFIFO',
          cohort: 'None',
          status: 'Active',
          statusMessage: 'Can admit new workloads',
          lastTransitionTime: '2026-06-18T10:49:48Z',
          admittedWorkloads: 1,
          reservingWorkloads: 0,
          pendingWorkloads: 0,
          localQueues: [{ name: 'cpu-queue', namespace: 'cpu-queue' }],
          flavors: [
            {
              name: 'default-flavor',
              resourceName: 'cpu',
              nominalQuota: '10',
              inUse: '2',
              reserved: '2',
              freeCapacity: '8',
              largestSlot: '8',
            },
          ],
        },
      ],
    });
  }

  /**
   * Fetches the list of pending and running jobs for a given queue.
   */
  getQueueJobs(queueName?: string): Observable<QueueJobListResponse> {
    // TODO(#186): Switch to real backend endpoint when rod-data-provider is deployed.
    return of({
      queueName: queueName || 'cpu-cluster-queue',
      jobs: [
        {
          position: '#1 (Running)',
          xid: '46648',
          workUnitId: '1',
          jobName: 'xmc-operation-46648-1',
          user: 'janzak@google.com',
          resources: 'CPU: 500m',
          priority: 'High',
          status: 'RUNNING',
          timeInQueue: '5m ago',
          queueName: queueName || 'cpu-cluster-queue',
        },
      ],
    });
  }
}
