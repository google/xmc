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

import {
  BatchGetQueueSecondaryDataResponse,
  GetFittingQueuesResponse,
  GetRunningAndPendingJobsResponse,
  ListClusterQueuesResponse,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import { cleanState, flush, get, setupModule, setupObservableMatchers } from '../testing/test';
import { XMC_API_DOMAIN } from './constants';
import { ResourceService } from './resource_service';

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
    it('should fetch cluster queues directly matching ListClusterQueuesResponse proto shape', async () => {
      const mockResponse: ListClusterQueuesResponse = {
        queues: [
          {
            queueName: 'gpu-cluster-queue',
            queueCapacity: [
              {
                resourceType: 'nvidia.com/gpu',
                totalCapacity: 64,
                freeCapacity: 16,
              },
            ],
          },
        ],
      };

      const responsePromise: Promise<ListClusterQueuesResponse> = firstValueFrom(
        state.service.getClusterQueues(),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/clusterqueues`);
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response.queues?.length).toBe(1);
      expect(response.queues?.[0].queueName).toBe('gpu-cluster-queue');
      expect(response.queues?.[0].queueCapacity?.[0].resourceType).toBe('nvidia.com/gpu');
      expect(response.queues?.[0].queueCapacity?.[0].totalCapacity).toBe(64);
    });

    it('should handle HTTP error gracefully and return empty queues response', async () => {
      const responsePromise: Promise<ListClusterQueuesResponse> = firstValueFrom(
        state.service.getClusterQueues(),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/clusterqueues`);
      req.flush('Server error', { status: 500, statusText: 'Internal Server Error' });

      const response = await responsePromise;
      expect(response.queues).toEqual([]);
      await flush();
    });
  });

  describe('getQueueJobs', () => {
    it('should fetch queue jobs list for given queue', async () => {
      const mockResponse: GetRunningAndPendingJobsResponse = {
        jobs: [
          {
            positionInQueue: 1,
            xid: 101,
            status: 'RUNNING',
          },
        ],
        totalQueueSize: 1,
      };

      const responsePromise: Promise<GetRunningAndPendingJobsResponse> = firstValueFrom(
        state.service.getQueueJobs('custom-queue'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/clusterqueues/custom-queue/jobs`);
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response.jobs?.length).toBe(1);
      expect(response.jobs?.[0].xid).toBe(101);
      await flush();
    });

    it('should handle HTTP error gracefully and return empty jobs response', async () => {
      const responsePromise: Promise<GetRunningAndPendingJobsResponse> = firstValueFrom(
        state.service.getQueueJobs('custom-queue'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/clusterqueues/custom-queue/jobs`);
      req.flush('Server error', { status: 500, statusText: 'Internal Server Error' });

      const response = await responsePromise;
      expect(response.jobs).toEqual([]);
      expect(response.totalQueueSize).toBe(0);
      await flush();
    });
  });

  describe('getQueueSecondaryData', () => {
    it('should fetch secondary queue data via HTTP POST', async () => {
      const mockResponse = {
        queuePredictability: {
          'cq-gpu': {
            queuePredictability: [
              {
                jobVelocity: 12.5,
                resourceVelocity: { resourceType: 'GPU H100', velocity: 1200 },
                velocityRange: 'RANGE_PRESET_LAST_HOUR' as const,
              },
            ],
          },
        },
      };

      const responsePromise: Promise<BatchGetQueueSecondaryDataResponse> = firstValueFrom(
        state.service.getQueueSecondaryData(['cq-gpu'], 'RANGE_PRESET_LAST_HOUR'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/clusterqueues:batchGetSecondaryData`);
      expect(req.request.method).toBe('POST');
      expect(req.request.body).toEqual({ queueNames: ['cq-gpu'], range: 'RANGE_PRESET_LAST_HOUR' });
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response.queuePredictability?.['cq-gpu']?.queuePredictability?.length).toBe(1);
      expect(response.queuePredictability?.['cq-gpu']?.queuePredictability?.[0].jobVelocity).toBe(12.5);
      await flush();
    });

    it('should return empty queuePredictability immediately when queueNames is empty', async () => {
      const response: BatchGetQueueSecondaryDataResponse = await firstValueFrom(state.service.getQueueSecondaryData([]));
      expect(response.queuePredictability).toEqual({});
    });
  });
});
