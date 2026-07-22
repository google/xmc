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
import { Observable } from 'rxjs';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
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
  getClusterQueues(period?: string): Observable<ClusterQueueListResponse> {
    const params = period ? `?period=${encodeURIComponent(period)}` : '';
    return this.http.get<ClusterQueueListResponse>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues${params}`,
      {
        withCredentials: true,
      },
    );
  }

  /**
   * Fetches the list of pending and running jobs for a given queue.
   */
  getQueueJobs(queueName?: string): Observable<QueueJobListResponse> {
    const params = queueName ? `?queue=${encodeURIComponent(queueName)}` : '';
    return this.http.get<QueueJobListResponse>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues/jobs${params}`,
      {
        withCredentials: true,
      },
    );
  }
}
