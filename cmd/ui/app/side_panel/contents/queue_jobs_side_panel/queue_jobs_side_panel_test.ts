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
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';
import { bootstrap, cleanState, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { QueueJobsSidePanel, QueueJobsSidePanelData } from './queue_jobs_side_panel';

const TEST_QUEUE_JOBS_RESP = {
  queueName: 'cpu-cluster-queue',
  jobs: [
    {
      position: 'N/A',
      xid: '1',
      workUnitId: '1',
      jobName: 'job-1',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'RUNNING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: 'N/A',
      xid: '1',
      workUnitId: '2',
      jobName: 'job-2',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'RUNNING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: 'N/A',
      xid: '1',
      workUnitId: '3',
      jobName: 'job-3',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'RUNNING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: 'N/A',
      xid: '1',
      workUnitId: '4',
      jobName: 'job-4',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'RUNNING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: 'N/A',
      xid: '1',
      workUnitId: '5',
      jobName: 'job-5',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'RUNNING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: '#1',
      xid: '1',
      workUnitId: '6',
      jobName: 'job-6',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'PENDING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: '#2',
      xid: '1',
      workUnitId: '7',
      jobName: 'job-7',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'PENDING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: '#3',
      xid: '1',
      workUnitId: '8',
      jobName: 'job-8',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'PENDING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: '#4',
      xid: '1',
      workUnitId: '9',
      jobName: 'job-9',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'PENDING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
    {
      position: '#5',
      xid: '1',
      workUnitId: '10',
      jobName: 'job-10',
      user: 'u',
      resources: 'c',
      priority: 'H',
      status: 'PENDING',
      timeInQueue: '5m',
      queueName: 'cpu-cluster-queue',
    },
  ],
};

describe('QueueJobsSidePanel Component', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    const sidePanelService = jasmine.createSpyObj<SidePanelService>('SidePanelService', [
      'closeSidePanel',
    ]);

    resourceService.getQueueJobs.and.returnValue(of(TEST_QUEUE_JOBS_RESP));

    setupModule({
      imports: [QueueJobsSidePanel, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ResourceService, useValue: resourceService },
        { provide: SidePanelService, useValue: sidePanelService },
      ],
    });

    return {
      resourceService,
      sidePanelService,
    };
  }, beforeEach);

  it('should be created and load queue jobs', () => {
    const comp = bootstrap(QueueJobsSidePanel, {
      data: new QueueJobsSidePanelData('cpu-cluster-queue'),
    });

    expect(comp).toBeTruthy();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('cpu-cluster-queue');
    expect(comp.totalCount()).toBe(10);
  });

  it('should close side panel when close() is called', () => {
    const comp = bootstrap(QueueJobsSidePanel, {
      data: new QueueJobsSidePanelData('cpu-cluster-queue'),
    });

    comp.close();
    expect(state.sidePanelService.closeSidePanel).toHaveBeenCalled();
  });

  it('should filter jobs based on status filter', () => {
    const comp = bootstrap(QueueJobsSidePanel, {
      data: new QueueJobsSidePanelData('cpu-cluster-queue'),
    });

    comp.statusFilter.set('RUNNING');
    expect(comp.filteredJobs().length).toBe(5);

    comp.statusFilter.set('PENDING');
    expect(comp.filteredJobs().length).toBe(5);
  });
});
