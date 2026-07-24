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

import { QueueListComponent } from './queue_list';

@Component({
  standalone: true,
  imports: [QueueListComponent],
  template: `
    <xmc-rod-queue-list
      [queues]="queues()"
      [selectedQueue]="selectedQueue()"
      [isLoading]="isLoading()"
      [(velocityPeriod)]="velocityPeriod"
      (selectQueue)="selectedQueue.set($event)"
    />
  `,
})
class TestHostComponent {
  readonly queues = signal<ClusterQueue[]>([
    {
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
      flavors: [],
    },
    {
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
      flavors: [],
    },
  ]);
  readonly selectedQueue = signal<string>('gpu-cluster-queue');
  readonly isLoading = signal<boolean>(false);
  readonly velocityPeriod = signal<string>('1h');
}

describe('QueueListComponent', () => {
  beforeAll(setupObservableMatchers);

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
    });
    return {};
  }, beforeEach);

  it('should render queue list and respond to velocity timeframe toggle clicks', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const buttons = document.querySelectorAll('.toggle-pill');
    expect(buttons.length).toBe(3);
    expect(buttons[0].classList.contains('active-toggle')).toBeTrue();

    (buttons[1] as HTMLElement).click();
    await flush();

    expect(host.velocityPeriod()).toBe('24h');
    expect(buttons[0].classList.contains('active-toggle')).toBeFalse();
    expect(buttons[1].classList.contains('active-toggle')).toBeTrue();
  });

  it('should select queue when a queue card is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const cards = document.querySelectorAll('.queue-card');
    expect(cards.length).toBe(2);
    expect(cards[0].classList.contains('selected')).toBeTrue();
    expect(cards[1].classList.contains('selected')).toBeFalse();

    (cards[1] as HTMLElement).click();
    await flush();

    expect(host.selectedQueue()).toBe('cpu-cluster-queue');
    expect(cards[0].classList.contains('selected')).toBeFalse();
    expect(cards[1].classList.contains('selected')).toBeTrue();
  });
});
