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
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
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
    };
  }, beforeEach);

  describe('getClusterQueues', () => {
    it('should return cluster queues dummy data', async () => {
      const response: ClusterQueueListResponse = await firstValueFrom(
        state.service.getClusterQueues(),
      );
      expect(response.clusterQueues.length).toBe(1);
      expect(response.clusterQueues[0].name).toBe('gpu-cluster-queue');
    });
  });

  describe('getQueueJobs', () => {
    it('should return queue jobs dummy data', async () => {
      const response: QueueJobListResponse = await firstValueFrom(
        state.service.getQueueJobs('gpu-cluster-queue'),
      );
      expect(response.jobs.length).toBe(2);
      expect(response.jobs[0].jobName).toBe('xmc-operation-53296-1');
    });
  });
});
