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
import { catchError } from 'rxjs/operators';

import {
  GetFittingQueuesResponse,
  QueueJobListResponse,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

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
   * Fetches the list of cluster queues strictly matching the GetFittingQueuesResponse proto shape.
   */
  getClusterQueues(): Observable<GetFittingQueuesResponse> {
    return this.http
      .get<GetFittingQueuesResponse>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues`,
        { withCredentials: true },
      )
      .pipe(
        catchError((error) => {
          console.error('Failed to fetch cluster queues:', error);
          return of({ queues: [] });
        }),
      );
  }

  /**
   * Fetches the list of pending and running jobs for a given queue.
   */
  getQueueJobs(queueName?: string): Observable<QueueJobListResponse> {
    // TODO(#186): Switch to real backend endpoint when rod-data-provider is deployed.
    const targetQueueName = queueName ?? 'gpu-cluster-queue';
    return of({
      queueName: targetQueueName,
      jobs: [
        {
          position: 'N/A-running',
          xid: '53296',
          workUnitId: '1',
          jobName: 'xmc-operation-53296-1',
          user: 'example@example.com',
          resources: 'GPU: 8, CPU: 32',
          priority: 'Low',
          status: 'Running',
          timeInQueue: '10m ago',
          queueName: targetQueueName,
        },
        {
          position: 'N/A-running',
          xid: '53308',
          workUnitId: '1',
          jobName: 'xmc-operation-53308-1',
          user: 'example@example.com',
          resources: 'GPU: 8, CPU: 32',
          priority: 'Low',
          status: 'Running',
          timeInQueue: '5m ago',
          queueName: targetQueueName,
        },
      ],
    });
  }
}
