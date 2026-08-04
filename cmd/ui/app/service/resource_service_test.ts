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

import 'jasmine';

import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
import { ResourceService } from './resource_service';
import { ClusterQueueListResponse, QueueJobListResponse } from './resource_types';

describe('ResourceService', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), provideHttpClientTesting(), ResourceService],
    });

    return {
      service: get(ResourceService),
      httpMock: get(HttpTestingController),
    };
  }, beforeEach);

  afterEach(() => {
    state.httpMock.verify();
  });

  describe('getClusterQueues', () => {
    it('should fetch cluster queues from backend and map capacities to flavors', async () => {
      const mockResponse = {
        queues: [
          {
            queueName: 'gpu-cluster-queue',
            queueCapacity: [
              {
                resourceType: 'nvidia-tesla-h100',
                totalCapacity: 64,
                freeCapacity: 16,
              },
            ],
          },
        ],
      };

      const responsePromise: Promise<ClusterQueueListResponse> = firstValueFrom(
        state.service.getClusterQueues(),
      );

      const req = state.httpMock.expectOne(`${EXPERIMENT_STATE_SERVER_DOMAIN}/queues/fitting`);
      expect(req.request.method).toBe('POST');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response.clusterQueues.length).toBe(1);
      expect(response.clusterQueues[0].name).toBe('gpu-cluster-queue');
      expect(response.clusterQueues[0].flavors.length).toBe(1);
      expect(response.clusterQueues[0].flavors[0].name).toBe('nvidia-tesla-h100');
      expect(response.clusterQueues[0].flavors[0].nominalQuota).toBe('64');
      expect(response.clusterQueues[0].flavors[0].freeCapacity).toBe('16');
      expect(response.clusterQueues[0].flavors[0].inUse).toBe('48');
    });
  });

  describe('getQueueJobs', () => {
    it('should return empty queue jobs list', async () => {
      const response: QueueJobListResponse = await firstValueFrom(
        state.service.getQueueJobs('gpu-cluster-queue'),
      );
      expect(response.jobs.length).toBe(0);
      expect(response.queueName).toBe('gpu-cluster-queue');
    });
  });
});
