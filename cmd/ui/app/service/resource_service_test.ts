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
import { GetFittingQueuesResponse } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
import { ResourceService } from './resource_service';
import { QueueJobListResponse } from './resource_types';

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
    it('should fetch cluster queues directly matching GetFittingQueuesResponse proto shape', async () => {
      const mockResponse: GetFittingQueuesResponse = {
        queues: [
          {
            queueName: 'gpu-cluster-queue',
            queueCapacity: [
              {
                resourceType: 'nvidia-tesla-h100',
                totalCapacity: 64,
                freeCapacity: 16,
                freeCapacityWithBorrowing: 16,
              },
            ],
          },
        ],
      };

      const responsePromise: Promise<GetFittingQueuesResponse> = firstValueFrom(
        state.service.getClusterQueues(),
      );

      const req = state.httpMock.expectOne(`${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues`);
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response.queues?.length).toBe(1);
      expect(response.queues?.[0].queueName).toBe('gpu-cluster-queue');
      expect(response.queues?.[0].queueCapacity?.[0].resourceType).toBe('nvidia-tesla-h100');
      expect(response.queues?.[0].queueCapacity?.[0].totalCapacity).toBe(64);
      expect(response.queues?.[0].queueCapacity?.[0].freeCapacity).toBe(16);
    });
  });

  describe('getQueueJobs', () => {
    it('should return mock queue jobs list for given queue', async () => {
      const response: QueueJobListResponse = await firstValueFrom(
        state.service.getQueueJobs('custom-queue'),
      );
      expect(response.jobs.length).toBe(2);
      expect(response.queueName).toBe('custom-queue');
      expect(response.jobs[0].queueName).toBe('custom-queue');
    });

    it('should fallback to default queueName when parameter is omitted', async () => {
      const response: QueueJobListResponse = await firstValueFrom(
        state.service.getQueueJobs(),
      );
      expect(response.queueName).toBe('gpu-cluster-queue');
    });
  });
});
