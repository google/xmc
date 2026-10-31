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

import { Component } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { BehaviorSubject, of, Subject, throwError } from 'rxjs';

import { ListClusterQueuesResponse, QueueOverview } from 'app/protos/rod';
import { AUTO_REFRESH_INTERVAL_MS } from 'app/service/constants';
import { ResourceService } from 'app/service/resource_service';
import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
  setupObservableMatchers,
} from 'app/testing/test';

import { AutoReloadService } from '../common/components/autoreloader/autoreloader_service';
import { ResourceDashboard } from './resource_dashboard';
import { ResourceDashboardHarness } from './resource_dashboard_harness';

@Component({
  standalone: true,
  imports: [ResourceDashboard],
  template: `<xmc-resource-dashboard />`,
})
class TestHostComponent {}

describe('ResourceDashboard Container', () => {
  beforeAll(setupObservableMatchers);

  const mockGpuQueue: QueueOverview = {
    queueName: 'gpu-cluster-queue',
    queueCapacity: [
      {
        resourceType: 'nvidia.com/gpu',
        totalCapacity: 64,
        freeCapacity: 56,
        usedCapacity: 8,
        freeCapacityWithBorrowing: 72,
      },
    ],
  };

  const mockCpuQueue: QueueOverview = {
    queueName: 'cpu-cluster-queue',
    queueCapacity: [
      {
        resourceType: 'cpu',
        totalCapacity: 512,
        freeCapacity: 448,
      },
    ],
  };

  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getClusterQueues',
      'getQueueJobs',
      'getQueueSecondaryData',
    ]);
    resourceService.getClusterQueues.and.returnValue(
      of({ queues: [mockGpuQueue, mockCpuQueue] } as ListClusterQueuesResponse),
    );
    resourceService.getQueueJobs.and.returnValue(of({ jobs: [], totalQueueSize: 0 }));
    resourceService.getQueueSecondaryData.and.returnValue(of({ queuePredictability: {} }));

    const reload$ = new Subject<void>();
    const queryParams$ = new BehaviorSubject({ queue: 'gpu-cluster-queue' });
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        {
          provide: AutoReloadService,
          useValue: {
            createReloader: () => ({ reloader$: reload$, start: () => {}, stop: () => {} }),
          },
        },
        { provide: ResourceService, useValue: resourceService },
        { provide: AUTO_REFRESH_INTERVAL_MS, useValue: 30000 },
        {
          provide: ActivatedRoute,
          useValue: {
            queryParams: queryParams$,
            data: of({}),
          },
        },
      ],
    });
    return { resourceService, reload$, queryParams$ };
  }, beforeEach);

  it('should render subnav tabs, smart resource finder, resolved cluster queues, and queue jobs table', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const finderHarness = (await dashboard.getSmartResourceFinder())!;
    await finderHarness.setAmountValue('50');
    await flush();

    const queueListHarness = (await dashboard.getQueueList())!;
    expect(await queueListHarness.getQueueCardCount()).toBe(2);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('gpu-cluster-queue');

    expect(await finderHarness.getResourceTypeSelectCount()).toBe(1);
    expect(await dashboard.getQueueJobsTable()).not.toBeNull();
  });

  it('should show quota usage and borrowing headroom for the selected queue', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaRows()).toEqual([['nvidia.com/gpu', '8', '56', '64', '72']]);
    expect(await dashboard.getGpuQuotaText()).toContain('GPU quota: gpu-cluster-queue');
    expect(await dashboard.getGpuQuotaText()).toContain('Quota accounting');

    const queueList = (await dashboard.getQueueList())!;
    await (await queueList.getQueueCard(1)).click();
    await flush();
    expect(await dashboard.getGpuQuotaRows()).toEqual([]);
    expect(await dashboard.getGpuQuotaText()).toContain('GPU quota: cpu-cluster-queue');
    expect(await dashboard.getGpuQuotaText()).toContain('No GPU quota configured');
  });

  it('should keep GPU types separate and report borrowed usage above nominal quota', async () => {
    state.resourceService.getClusterQueues.and.returnValue(
      of({
        queues: [
          {
            ...mockGpuQueue,
            queueCapacity: [
              {
                resourceType: 'nvidia.com/gpu: nvidia-h100',
                totalCapacity: 8,
                freeCapacity: 0,
                usedCapacity: 12,
                freeCapacityWithBorrowing: 4,
              },
              {
                resourceType: 'nvidia.com/gpu: nvidia-l4',
                totalCapacity: 16,
                freeCapacity: 16,
                usedCapacity: 0,
                freeCapacityWithBorrowing: 16,
              },
              { resourceType: 'cpu', totalCapacity: 64, freeCapacity: 32, usedCapacity: 32 },
            ],
          },
        ],
      }),
    );
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaRows()).toEqual([
      ['nvidia.com/gpu: nvidia-h100', '12', '0', '8', '4'],
      ['nvidia.com/gpu: nvidia-l4', '0', '16', '16', '16'],
    ]);
  });

  it('should show unavailable usage when the server does not report it', async () => {
    state.resourceService.getClusterQueues.and.returnValue(
      of({
        queues: [
          {
            ...mockGpuQueue,
            queueCapacity: [{ resourceType: 'nvidia.com/gpu', totalCapacity: 8, freeCapacity: 0 }],
          },
        ],
      }),
    );
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaRows()).toEqual([
      ['nvidia.com/gpu', 'Unavailable', '0', '8', 'Unavailable'],
    ]);
  });

  it('should show unavailable quota when the selected queue is absent', async () => {
    state.resourceService.getClusterQueues.and.returnValue(of({ queues: [] }));
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaText()).toContain('Queue quota unavailable.');
  });

  it('should refresh quota and retain the last data when refresh fails', async () => {
    bootstrap(TestHostComponent);
    await flush();
    const dashboard = await getHarness(ResourceDashboardHarness);
    state.resourceService.getClusterQueues.and.returnValue(throwError(() => new Error('offline')));
    state.reload$.next();
    await flush();
    expect(await dashboard.getGpuQuotaText()).toContain('Queue quota refresh failed');
    expect(await dashboard.getGpuQuotaRows()).toEqual([['nvidia.com/gpu', '8', '56', '64', '72']]);

    const refreshed$ = new Subject<ListClusterQueuesResponse>();
    state.resourceService.getClusterQueues.and.returnValue(refreshed$);
    state.reload$.next();
    await flush();
    expect(await dashboard.getGpuQuotaText()).toContain('Refreshing queue quota');
    refreshed$.next({
      queues: [
        { ...mockGpuQueue, queueCapacity: [{ resourceType: 'nvidia.com/gpu', usedCapacity: 12 }] },
      ],
    });
    await flush();
    expect(await dashboard.getGpuQuotaRows()).toEqual([
      ['nvidia.com/gpu', '12', 'Unavailable', 'Unavailable', 'Unavailable'],
    ]);
    expect(await dashboard.getGpuQuotaText()).not.toContain('Queue quota refresh failed');
    expect(await dashboard.getGpuQuotaText()).not.toContain('Refreshing queue quota');
  });

  it('should select a configured queue when the URL does not select one', async () => {
    state.queryParams$.next({ queue: '' });
    state.resourceService.getClusterQueues.and.returnValue(of({ queues: [mockCpuQueue] }));
    bootstrap(TestHostComponent);
    await flush();
    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaText()).toContain('GPU quota: cpu-cluster-queue');
  });

  it('should wait for a configured queue before fetching jobs', async () => {
    state.queryParams$.next({ queue: '' });
    state.resourceService.getClusterQueues.and.returnValue(of({ queues: [] }));
    bootstrap(TestHostComponent);
    await flush();
    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getQueueJobsTable()).toBeNull();
    expect(state.resourceService.getQueueJobs).not.toHaveBeenCalled();
  });

  it('should preserve an unavailable explicit queue selection', async () => {
    state.queryParams$.next({ queue: 'missing-queue' });
    bootstrap(TestHostComponent);
    await flush();
    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getGpuQuotaText()).toContain('GPU quota: missing-queue');
    expect(await dashboard.getGpuQuotaText()).toContain('Queue quota unavailable.');
  });

  it('should populate picker dropdown with correct available resource types sorted alphabetically', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const finderHarness = (await dashboard.getSmartResourceFinder())!;
    const options = await finderHarness.getResourceTypeOptions();
    expect(options).toEqual(['-', 'cpu', 'nvidia.com/gpu']);
  });

  it('should filter queue list by resource type selection in the picker', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const queueListHarness = (await dashboard.getQueueList())!;
    const finderHarness = (await dashboard.getSmartResourceFinder())!;

    await finderHarness.setAmountValue('50');
    await flush();
    expect(await queueListHarness.getQueueCardCount()).toBe(2);

    await finderHarness.selectResourceType(0, 'cpu');
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(1);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('cpu-cluster-queue');
  });

  it('should filter queue list by amount requirement in the picker', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const queueListHarness = (await dashboard.getQueueList())!;
    const finderHarness = (await dashboard.getSmartResourceFinder())!;

    await finderHarness.setAmountValue('50');
    await flush();
    expect(await queueListHarness.getQueueCardCount()).toBe(2);

    await finderHarness.setAmountValue('100');
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(1);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('cpu-cluster-queue');
  });

  it('should filter queue list by both resource type and amount requirements', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const queueListHarness = (await dashboard.getQueueList())!;
    const finderHarness = (await dashboard.getSmartResourceFinder())!;

    await finderHarness.setAmountValue('32');
    await finderHarness.selectResourceType(0, 'nvidia.com/gpu');
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(1);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('gpu-cluster-queue');

    await finderHarness.setAmountValue('100');
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(0);
    expect(await queueListHarness.isEmpty()).toBeTrue();
  });

  it('should switch tabs to Resource availability and render an empty tab without queue list', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const queueListHarness = (await dashboard.getQueueList())!;
    expect(await queueListHarness.getQueueCardCount()).toBe(2);

    const tabsHarness = await dashboard.getSubnavTabs();
    expect(await tabsHarness.isTabActive(0)).toBeTrue();
    expect(await tabsHarness.isTabActive(1)).toBeFalse();

    await tabsHarness.clickTab(1);
    await flush();

    expect(await tabsHarness.isTabActive(0)).toBeFalse();
    expect(await tabsHarness.isTabActive(1)).toBeTrue();
    expect(await dashboard.getQueueList()).toBeNull();
    expect(await dashboard.getQueueJobsTable()).toBeNull();
    const wipBanner = await dashboard.getWipBanner();
    expect(wipBanner).not.toBeNull();
    expect(await wipBanner!.getText()).toBe('Work in Progress');

    await tabsHarness.clickTab(0);
    await flush();

    expect(await tabsHarness.isTabActive(0)).toBeTrue();
    expect(await tabsHarness.isTabActive(1)).toBeFalse();
    const restoredQueueList = (await dashboard.getQueueList())!;
    expect(await restoredQueueList.getQueueCardCount()).toBe(2);
    expect(await dashboard.getQueueJobsTable()).not.toBeNull();
  });

  it('should select queue when a queue card is clicked', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const queueListHarness = (await dashboard.getQueueList())!;
    const card1 = await queueListHarness.getQueueCard(1);

    expect(await card1.isSelected()).toBeFalse();
    await card1.click();
    await flush();

    expect(await card1.isSelected()).toBeTrue();
  });
});
