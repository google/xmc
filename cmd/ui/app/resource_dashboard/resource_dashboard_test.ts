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

import { Component } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { of } from 'rxjs';

import { AUTO_REFRESH_INTERVAL_MS } from 'xmanager_cloud/ui/app/service/constants';
import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';
import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { QueueListHarness } from './components/queue_list/queue_list_harness';
import { SmartResourceFinderHarness } from './components/smart_resource_finder/smart_resource_finder_harness';
import { ResourceDashboard } from './resource_dashboard';

const SUBNAV_TAB_SELECTOR = '.subnav-tab';

@Component({
  standalone: true,
  imports: [ResourceDashboard],
  template: `<xmc-resource-dashboard />`,
})
class TestHostComponent {}

describe('ResourceDashboard Container', () => {
  beforeAll(setupObservableMatchers);

  const mockGpuQueue = {
    name: 'gpu-cluster-queue',
    strategy: 'BestEffortFIFO',
    cohort: 'gpu-cohort',
    status: 'Active',
    statusMessage: 'Can admit new workloads',
    lastTransitionTime: '2026-07-22T10:00:00Z',
    admittedWorkloads: 1,
    reservingWorkloads: 0,
    pendingWorkloads: 0,
    pendingJobsCount: 0,
    admittedJobsCount: 1,
    dominantResource: 'GPU H100',
    dominantResourceUnits: 8,
    requestedResources: '0',
    localQueues: [{ name: 'gpu-queue', namespace: 'default' }],
    flavors: [
      {
        name: 'h100-flavor',
        resourceName: 'nvidia.com/gpu',
        nominalQuota: '64',
        inUse: '8',
        reserved: '0',
        freeCapacity: '56',
        largestSlot: '56 → 64',
      },
    ],
  } as ClusterQueue;

  const mockCpuQueue = {
    name: 'cpu-cluster-queue',
    strategy: 'BestEffortFIFO',
    cohort: 'cpu-cohort',
    status: 'Active',
    statusMessage: 'Can admit new workloads',
    lastTransitionTime: '2026-07-22T10:00:00Z',
    admittedWorkloads: 2,
    reservingWorkloads: 0,
    pendingWorkloads: 0,
    pendingJobsCount: 0,
    admittedJobsCount: 2,
    dominantResource: 'CPU',
    dominantResourceUnits: 32,
    requestedResources: '0',
    localQueues: [{ name: 'cpu-queue', namespace: 'default' }],
    flavors: [
      {
        name: 'cpu-flavor',
        resourceName: 'cpu',
        nominalQuota: '512',
        inUse: '64',
        reserved: '0',
        freeCapacity: '448',
        largestSlot: '448 → 512',
      },
    ],
  } as ClusterQueue;

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
      providers: [
        { provide: AUTO_REFRESH_INTERVAL_MS, useValue: 30000 },
        {
          provide: ActivatedRoute,
          useValue: {
            queryParams: of({ queue: 'gpu-cluster-queue' }),
            data: of({ clusterQueues: [mockGpuQueue, mockCpuQueue] }),
          },
        },
      ],
    });
    return {};
  }, beforeEach);

  it('should render subnav tabs, smart resource finder, and resolved cluster queues', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const queueListHarness = (await getHarness(QueueListHarness)) as QueueListHarness;
    expect(await queueListHarness.getQueueCardCount()).toBe(2);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('gpu-cluster-queue');

    const finderHarness = (await getHarness(
      SmartResourceFinderHarness,
    )) as SmartResourceFinderHarness;
    expect(await finderHarness.getResourceTypeSelectCount()).toBe(1);
  });

  it('should filter queue list when finding matching queues in smart resource finder', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const queueListHarness = (await getHarness(QueueListHarness)) as QueueListHarness;
    const finderHarness = (await getHarness(
      SmartResourceFinderHarness,
    )) as SmartResourceFinderHarness;

    expect(await queueListHarness.getQueueCardCount()).toBe(2);

    await finderHarness.selectResourceType(0, 'cpu');
    await finderHarness.clickFindButton();
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(1);
    expect(await (await queueListHarness.getQueueCard(0)).getQueueName()).toBe('cpu-cluster-queue');
  });

  it('should switch tabs and show all queues in resource availability tab', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const queueListHarness = (await getHarness(QueueListHarness)) as QueueListHarness;
    const finderHarness = (await getHarness(
      SmartResourceFinderHarness,
    )) as SmartResourceFinderHarness;

    await finderHarness.selectResourceType(0, 'cpu');
    await finderHarness.clickFindButton();
    await flush();
    expect(await queueListHarness.getQueueCardCount()).toBe(1);

    const tabs = document.querySelectorAll(SUBNAV_TAB_SELECTOR);
    (tabs[1] as HTMLButtonElement).click();
    await flush();

    expect(await queueListHarness.getQueueCardCount()).toBe(2);
  });

  it('should select queue when a queue card is clicked', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const queueListHarness = (await getHarness(QueueListHarness)) as QueueListHarness;
    const card1 = await queueListHarness.getQueueCard(1);

    expect(await card1.isSelected()).toBeFalse();
    await card1.click();
    await flush();

    expect(await card1.isSelected()).toBeTrue();
  });
});
