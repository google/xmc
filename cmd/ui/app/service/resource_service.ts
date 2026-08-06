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
  GetRunningAndPendingJobsResponse,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import { XMC_API_DOMAIN } from './constants';

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
      .get<GetFittingQueuesResponse>(`${XMC_API_DOMAIN}/clusterqueues`, {
        withCredentials: true,
      })
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
  getQueueJobs(queueName?: string): Observable<GetRunningAndPendingJobsResponse> {
    const targetQueueName = queueName ?? 'cpu-cluster-queue';
    return this.http
      .get<GetRunningAndPendingJobsResponse>(
        `${XMC_API_DOMAIN}/clusterqueues/${encodeURIComponent(targetQueueName)}/jobs`,
        { withCredentials: true },
      )
      .pipe(
        catchError((error) => {
          console.error(`Failed to fetch jobs for queue ${targetQueueName}:`, error);
          return of({ jobs: [], totalQueueSize: 0, nextPageToken: '' });
        }),
      );
  }
}
