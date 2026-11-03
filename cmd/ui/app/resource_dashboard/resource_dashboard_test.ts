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
import { of } from 'rxjs';

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

    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        { provide: ResourceService, useValue: resourceService },
        { provide: AUTO_REFRESH_INTERVAL_MS, useValue: 30000 },
        {
          provide: ActivatedRoute,
          useValue: {
            queryParams: of({ queue: 'gpu-cluster-queue' }),
            data: of({}),
          },
        },
      ],
    });
    return { resourceService };
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

  it('should synthesize generic parent in dropdown and match specific compound queue when selected', async () => {
    const mockA100Queue: QueueOverview = {
      queueName: 'a100-gpu-queue',
      queueCapacity: [
        {
          resourceType: 'nvidia.com/gpu: nvidia-tesla-a100',
          totalCapacity: 64,
          freeCapacity: 56,
        },
      ],
    };
    state.resourceService.getClusterQueues.and.returnValue(
      of({ queues: [mockA100Queue, mockCpuQueue] } as ListClusterQueuesResponse),
    );

    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const finderHarness = (await dashboard.getSmartResourceFinder())!;
    const options = await finderHarness.getResourceTypeOptions();
    expect(options).toEqual(['-', 'cpu', 'nvidia.com/gpu', 'nvidia.com/gpu: nvidia-tesla-a100']);

    await finderHarness.setAmountValue('32');
    await finderHarness.selectResourceType(0, 'nvidia.com/gpu');
    await flush();

    const queueListHarness = (await dashboard.getQueueList())!;
    expect(await queueListHarness.getQueueCardCount()).toBe(1);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('a100-gpu-queue');
  });

  it('should integrate with ResizeDividerComponent for pointer drag, bounds clamping, dblclick, and keyboard resize', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const divider = (await dashboard.getResizeDivider())!;

    const attrs = await divider.getAttributes();
    expect(attrs.role).toBe('separator');
    expect(attrs.ariaLabel).toBe('Drag to resize sections (double-click to reset)');
    expect(attrs.title).toBe('Drag to resize sections (double-click to reset)');
    expect(attrs.ariaValueNow).toBe('50');

    await stubSplitContainerWidth(dashboard, 1000);

    expect(await dashboard.getLeftPaneStyle()).toContain('50%');
    expect(await dashboard.getRightPaneStyle()).toContain('50%');

    // Drag to 30% / 70%
    await divider.startDrag(500);
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300, bubbles: true }));
    await flush();
    expect(await dashboard.getLeftPaneStyle()).toContain('30%');
    expect(await dashboard.getRightPaneStyle()).toContain('70%');

    // Clamp maximum at 80% and release
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 950, bubbles: true }));
    window.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
    await flush();
    expect(await dashboard.getLeftPaneStyle()).toContain('80%');
    expect(await dashboard.getRightPaneStyle()).toContain('20%');

    // Double-click resets back to 50% / 50%
    await divider.doubleClick();
    await flush();
    expect(await dashboard.getLeftPaneStyle()).toContain('50%');
    expect(await dashboard.getRightPaneStyle()).toContain('50%');

    // Keyboard ArrowLeft / Enter integration
    await divider.pressKey('ArrowLeft');
    await flush();
    expect(await dashboard.getLeftPaneStyle()).toContain('45%');

    await divider.pressKey('Enter');
    await flush();
    expect(await dashboard.getLeftPaneStyle()).toContain('50%');
  });
});

async function stubSplitContainerWidth(
  dashboard: ResourceDashboardHarness,
  width: number,
): Promise<void> {
  const containerEl = (await dashboard.getSplitContainerNativeElement())!;
  spyOn(containerEl, 'getBoundingClientRect').and.returnValue({
    left: 0,
    width,
  } as DOMRect);
}
