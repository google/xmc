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

const TEST_CLUSTER_QUEUES_RESP = {
  clusterQueues: [
    {
      name: 'cpu-cluster-queue',
      strategy: 'BestEffortFIFO',
      cohort: 'None',
      status: 'Active',
      statusMessage: 'Can admit new workloads',
      lastTransitionTime: '2026-06-18T10:49:48Z',
      admittedWorkloads: 0,
      reservingWorkloads: 0,
      pendingWorkloads: 0,
      localQueues: [{ name: 'cpu-queue', namespace: 'cpu-queue' }],
      flavors: [],
    },
  ],
};

const TEST_QUEUE_JOBS_RESP = {
  queueName: 'cpu-queue',
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
      queueName: 'cpu-queue',
    },
  ],
};

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
    it('should make api call correctly and return response', async () => {
      const responsePromise = firstValueFrom(state.service.getClusterQueues());

      const req = state.httpMock.expectOne(`${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues`);
      expect(req.request.method).toBe('GET');

      req.flush(TEST_CLUSTER_QUEUES_RESP);

      const response = await responsePromise;
      expect(response).toEqual(TEST_CLUSTER_QUEUES_RESP);
    });

    it('should propagate http error on network failure', async () => {
      const responsePromise = firstValueFrom(state.service.getClusterQueues());

      const req = state.httpMock.expectOne(`${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues`);
      req.error(new ProgressEvent('Network error'));

      try {
        await responsePromise;
        fail('should have thrown error');
      } catch (err) {
        expect(err).toBeTruthy();
      }
    });
  });

  describe('getQueueJobs', () => {
    it('should make api call correctly and return response', async () => {
      const responsePromise = firstValueFrom(state.service.getQueueJobs('cpu-queue'));

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/clusterqueues/jobs?queue=cpu-queue`,
      );
      expect(req.request.method).toBe('GET');

      req.flush(TEST_QUEUE_JOBS_RESP);

      const response = await responsePromise;
      expect(response).toEqual(TEST_QUEUE_JOBS_RESP);
    });
  });
});
