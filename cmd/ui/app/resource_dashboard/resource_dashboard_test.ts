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
import { Component } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { AUTO_REFRESH_INTERVAL_MS } from 'xmanager_cloud/ui/app/service/constants';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';
import {
  bootstrap,
  cleanState,
  flush,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { ResourceDashboard } from './resource_dashboard';

@Component({
  standalone: true,
  imports: [ResourceDashboard],
  template: `<xmc-resource-dashboard />`,
})
class TestHostComponent {}

describe('ResourceDashboard Container', () => {
  beforeAll(setupObservableMatchers);

  const mockQueue = {
    name: 'gpu-cluster-queue',
    flavors: [],
  } as ClusterQueue;

  cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    resourceService.getQueueJobs.and.returnValue(of({ queueName: 'gpu-cluster-queue', jobs: [] }));

    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ResourceService, useValue: resourceService },
        { provide: AUTO_REFRESH_INTERVAL_MS, useValue: 30000 },
        {
          provide: ActivatedRoute,
          useValue: {
            queryParams: of({ queue: 'gpu-cluster-queue' }),
            data: of({ clusterQueues: [mockQueue] }),
          },
        },
      ],
    });
    return {};
  }, beforeEach);

  it('should render split container with queue jobs side panel', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    expect(host).toBeTruthy();

    const jobsPanel = document.querySelector('xmc-queue-jobs-side-panel');
    expect(jobsPanel).toBeTruthy();
  });
});
