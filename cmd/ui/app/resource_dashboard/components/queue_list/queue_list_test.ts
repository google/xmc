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

import { Component, signal } from '@angular/core';

import { QueueOverview } from 'app/protos/rod';
import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
  setupObservableMatchers,
} from 'app/testing/test';

import { QueueListComponent } from './queue_list';
import { QueueListHarness } from './queue_list_harness';

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
  readonly queues = signal<QueueOverview[]>([
    {
      queueName: 'gpu-cluster-queue',
      queueCapacity: [{ resourceType: 'nvidia-tesla-h100', totalCapacity: 64, freeCapacity: 16 }],
    },
    {
      queueName: 'cpu-cluster-queue',
      queueCapacity: [{ resourceType: 'cpu', totalCapacity: 128, freeCapacity: 64 }],
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
    const harness = (await getHarness(QueueListHarness)) as QueueListHarness;

    const labels = await harness.getVelocityToggleLabels();
    expect(labels).toEqual(['1h', '24h', '7d']);
    expect(await harness.getActiveVelocityToggleLabel()).toBe('1h');

    await harness.selectVelocityPeriod('24h');
    await flush();

    expect(host.velocityPeriod()).toBe('24h');
    expect(await harness.getActiveVelocityToggleLabel()).toBe('24h');
  });

  it('should select queue when a queue card is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    const harness = (await getHarness(QueueListHarness)) as QueueListHarness;

    expect(await harness.getQueueCardCount()).toBe(2);
    const card0 = await harness.getQueueCard(0);
    const card1 = await harness.getQueueCard(1);

    expect(await card0.isSelected()).toBeTrue();
    expect(await card1.isSelected()).toBeFalse();

    await card1.click();
    await flush();

    expect(host.selectedQueue()).toBe('cpu-cluster-queue');
    expect(await card0.isSelected()).toBeFalse();
    expect(await card1.isSelected()).toBeTrue();
  });
});
