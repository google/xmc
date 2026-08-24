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

import { HttpClient, HttpParams } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { catchError, Observable, of, throwError } from 'rxjs';

import {
  BatchGetQueueSecondaryDataResponse,
  GetRunningAndPendingJobsResponse,
  GetSubmitQueueResponse,
  ListClusterQueuesResponse,
  RangePreset,
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
   * Fetches the list of cluster queues strictly matching the ListClusterQueuesResponse proto shape.
   */
  getClusterQueues(): Observable<ListClusterQueuesResponse> {
    return this.http
      .get<ListClusterQueuesResponse>(`${XMC_API_DOMAIN}/clusterqueues`, {
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
  getQueueJobs(
    queueName: string,
    pageSize?: number,
    pageToken?: string,
  ): Observable<GetRunningAndPendingJobsResponse> {
    let params = new HttpParams();
    if (pageSize !== undefined && pageSize > 0) {
      params = params.set('page_size', pageSize.toString());
    }
    if (pageToken) {
      params = params.set('page_token', pageToken);
    }
    return this.http
      .get<GetRunningAndPendingJobsResponse>(
        `${XMC_API_DOMAIN}/clusterqueues/${encodeURIComponent(queueName)}/jobs`,
        { params, withCredentials: true },
      )
      .pipe(
        catchError((error) => {
          console.error(`Failed to fetch jobs for queue ${queueName}:`, error);
          return of({ jobs: [], totalQueueSize: 0, nextPageToken: '' });
        }),
      );
  }

  /**
   * Fetches the target submission queue (LocalQueue) for a given cluster queue.
   */
  getSubmitQueue(queueName: string): Observable<GetSubmitQueueResponse> {
    return this.http
      .get<GetSubmitQueueResponse>(
        `${XMC_API_DOMAIN}/clusterqueues/${encodeURIComponent(queueName)}/submitqueue`,
        { withCredentials: true },
      )
      .pipe(
        catchError((error) => {
          console.error(`Failed to fetch submit queue for ${queueName}:`, error);
          return of({ submitQueue: queueName });
        }),
      );
  }

  /**
   * Fetches secondary predictability telemetry data for the specified cluster queues.
   */
  getQueueSecondaryData(
    queueNames: string[],
    range: RangePreset = 'RANGE_PRESET_LAST_HOUR',
  ): Observable<BatchGetQueueSecondaryDataResponse> {
    if (!queueNames || queueNames.length === 0) {
      return of({ queuePredictability: {} });
    }
    return this.http
      .post<BatchGetQueueSecondaryDataResponse>(
        `${XMC_API_DOMAIN}/clusterqueues:batchGetSecondaryData`,
        { queueNames, range },
        { withCredentials: true },
      )
      .pipe(
        catchError((error) => {
          console.error('Failed to fetch secondary queue data:', error);
          return throwError(() => error);
        }),
      );
  }
}
