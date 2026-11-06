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

import {
  JOBS_FULL_WIDTH_STORAGE_KEY,
  readJobsFullWidthPreference,
  writeJobsFullWidthPreference,
} from './jobs_layout_preference';
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
    window.localStorage.removeItem(JOBS_FULL_WIDTH_STORAGE_KEY);
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

  afterEach(() => {
    window.localStorage.removeItem(JOBS_FULL_WIDTH_STORAGE_KEY);
  });

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

  it('should update the per-user allocation bar when the dominant resource changes in the picker', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const bar = await (await dashboard.getQueueJobsTable())!.getUserAllocationBar();
    expect(await bar.getHintText()).toContain('Pick a resource in "I need"');

    const finderHarness = (await dashboard.getSmartResourceFinder())!;
    await finderHarness.setAmountValue('1');
    await finderHarness.selectResourceType(0, 'nvidia.com/gpu');
    await flush();
    expect(await bar.getChipTexts()).toEqual([
      'alice@example.com 8 running + 4 pending GPUs',
      'bob@example.com 4 running GPUs',
    ]);

    await finderHarness.selectResourceType(0, 'cpu');
    await flush();
    expect(await bar.getChipTexts()).toEqual([
      'alice@example.com 32 running + 16 pending cores',
      'bob@example.com 8 running cores',
    ]);
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

  it('should bind the resize divider split ratio to both pane widths', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.getLeftPaneStyle()).toContain('50%');
    expect(await dashboard.getRightPaneStyle()).toContain('50%');

    const { left } = (await dashboard.getSplitContainerDimensions())!;
    const divider = (await dashboard.getResizeDivider())!;
    await divider.startDrag();
    // Dragging to the container edges clamps the split, which keeps the assertion layout-independent.
    await divider.moveDragTo(left);
    await divider.endDrag();
    expect(await dashboard.getLeftPaneStyle()).toContain('20%');
    expect(await dashboard.getRightPaneStyle()).toContain('80%');

    await divider.doubleClick();
    expect(await dashboard.getLeftPaneStyle()).toContain('50%');
    expect(await dashboard.getRightPaneStyle()).toContain('50%');
  });

  it('should toggle full-width mode via projected header action, stack panes vertically at 100% width, and persist preference to localStorage', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.isSplitContainerFullWidth()).toBeFalse();
    expect(readJobsFullWidthPreference()).toBeFalse();

    const { left } = (await dashboard.getSplitContainerDimensions())!;
    const divider = (await dashboard.getResizeDivider())!;
    await divider.startDrag();
    await divider.moveDragTo(left);
    await divider.endDrag();
    expect(await dashboard.getLeftPaneStyle()).toContain('20%');
    expect(await dashboard.getRightPaneStyle()).toContain('80%');

    const jobsTable = (await dashboard.getQueueJobsTable())!;
    expect(await jobsTable.getHeaderActionTexts()).toEqual(['fullscreen View in full width']);

    const toggleBtn = (await dashboard.getFullWidthToggleBtn())!;
    expect(toggleBtn).not.toBeNull();
    expect(await toggleBtn.getText()).toContain('View in full width');
    expect(await dashboard.getFullWidthToggleIconName()).toBe('fullscreen');
    expect(await dashboard.isFullWidthToggleActive()).toBeFalse();

    await toggleBtn.click();
    await flush();

    expect(await dashboard.isSplitContainerFullWidth()).toBeTrue();
    expect(readJobsFullWidthPreference()).toBeTrue();
    expect(await toggleBtn.getText()).toContain('Exit full width');
    expect(await dashboard.getFullWidthToggleIconName()).toBe('fullscreen_exit');
    expect(await dashboard.isFullWidthToggleActive()).toBeTrue();
    expect(await dashboard.getQueueList()).not.toBeNull();
    expect(await dashboard.getQueueJobsTable()).not.toBeNull();

    expect(await dashboard.getResizeDivider()).toBeNull();
    expect(await dashboard.getLeftPaneStyle()).not.toContain('%');
    expect(await dashboard.getRightPaneStyle()).not.toContain('%');

    const fullWidthDims = await dashboard.getSplitLayoutDimensions();
    expect(fullWidthDims.container).not.toBeNull();
    expect(fullWidthDims.mainContent).not.toBeNull();
    expect(fullWidthDims.jobsPane).not.toBeNull();
    expect(fullWidthDims.mainContent!.width).toBeCloseTo(fullWidthDims.container!.width, 0);
    expect(fullWidthDims.jobsPane!.width).toBeCloseTo(fullWidthDims.container!.width, 0);
    expect(fullWidthDims.mainContent!.top).toBeGreaterThanOrEqual(
      fullWidthDims.jobsPane!.top + fullWidthDims.jobsPane!.height,
    );

    await toggleBtn.click();
    await flush();

    expect(await dashboard.isSplitContainerFullWidth()).toBeFalse();
    expect(readJobsFullWidthPreference()).toBeFalse();
    expect(await toggleBtn.getText()).toContain('View in full width');
    expect(await dashboard.getFullWidthToggleIconName()).toBe('fullscreen');
    expect(await dashboard.isFullWidthToggleActive()).toBeFalse();
    expect(await dashboard.getResizeDivider()).not.toBeNull();
    expect(await dashboard.getLeftPaneStyle()).toContain('20%');
    expect(await dashboard.getRightPaneStyle()).toContain('80%');
  });

  it('should initialize in full-width mode when xmc-jobs-full-width=true is persisted in localStorage', async () => {
    writeJobsFullWidthPreference(true);

    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    expect(await dashboard.isSplitContainerFullWidth()).toBeTrue();

    const toggleBtn = (await dashboard.getFullWidthToggleBtn())!;
    expect(await toggleBtn.getText()).toContain('Exit full width');
    expect(await dashboard.getFullWidthToggleIconName()).toBe('fullscreen_exit');
    expect(await dashboard.isFullWidthToggleActive()).toBeTrue();
  });

  it('should preserve full-width mode across Smart Resource Finder searches and tab switches', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const dashboard = await getHarness(ResourceDashboardHarness);
    const toggleBtn = (await dashboard.getFullWidthToggleBtn())!;

    await toggleBtn.click();
    await flush();
    expect(await dashboard.isSplitContainerFullWidth()).toBeTrue();

    const finderHarness = (await dashboard.getSmartResourceFinder())!;
    await finderHarness.setAmountValue('100');
    await flush();
    expect(await dashboard.isSplitContainerFullWidth()).toBeTrue();
    expect(readJobsFullWidthPreference()).toBeTrue();

    const tabsHarness = await dashboard.getSubnavTabs();
    await tabsHarness.clickTab(1);
    await flush();
    await tabsHarness.clickTab(0);
    await flush();

    expect(await dashboard.isSplitContainerFullWidth()).toBeTrue();
    expect(readJobsFullWidthPreference()).toBeTrue();
  });
});
