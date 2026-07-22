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

import { Component, signal } from '@angular/core';

import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';
import {
  bootstrap,
  cleanState,
  flush,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { QueueCardComponent } from './queue_card';

@Component({
  standalone: true,
  imports: [QueueCardComponent],
  template: `
    <xmc-rod-queue-card
      [queue]="queue()"
      [isSelected]="isSelected()"
      (selectQueue)="selectedQueueName.set($event)"
    />
  `,
})
class TestHostComponent {
  readonly queue = signal<ClusterQueue>({
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
  });

  readonly isSelected = signal<boolean>(false);
  readonly selectedQueueName = signal<string>('');
}

describe('QueueCardComponent', () => {
  beforeAll(setupObservableMatchers);

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
    });
    return {};
  }, beforeEach);

  it('should render queue name and emit selection on click', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    host.isSelected.set(true);
    await flush();

    const card = document.querySelector('.queue-card') as HTMLElement;
    card.click();
    await flush();

    expect(host.selectedQueueName()).toBe('gpu-cluster-queue');
  });
});
