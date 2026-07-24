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
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { bootstrap, cleanState, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { QueueJobsSidePanel } from './queue_jobs_side_panel';

const TEST_QUEUE_JOBS_RESP = {
  queueName: 'gpu-cluster-queue',
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
      queueName: 'gpu-cluster-queue',
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
      queueName: 'gpu-cluster-queue',
    },
  ],
};

describe('QueueJobsSidePanel Component', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    resourceService.getQueueJobs.and.returnValue(of(TEST_QUEUE_JOBS_RESP));

    setupModule({
      imports: [QueueJobsSidePanel, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ResourceService, useValue: resourceService },
      ],
    });

    return {
      resourceService,
    };
  }, beforeEach);

  it('should be created and load queue jobs', () => {
    const comp = bootstrap(QueueJobsSidePanel, {
      inputs: { queueName: 'gpu-cluster-queue' },
    });

    expect(comp).toBeTruthy();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('gpu-cluster-queue');
  });
});
