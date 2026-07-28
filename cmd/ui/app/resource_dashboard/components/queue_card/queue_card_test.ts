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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';

import { QueueCardComponent } from './queue_card';
import { QueueCardHarness } from './queue_card_harness';

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
  let fixture: ComponentFixture<TestHostComponent>;
  let hostInstance: TestHostComponent;
  let harness: QueueCardHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    hostInstance = fixture.componentInstance;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(QueueCardHarness);
  });

  it('should render queue name and status badge', async () => {
    expect(await harness.getQueueName()).toBe('gpu-cluster-queue');
    expect(await harness.getStatusBadgeText()).toContain('Resources available');
    expect(await harness.isStatusBadgeHealthy()).toBeTrue();
  });

  it('should render flavor capacity rows', async () => {
    expect(await harness.getFlavorRowCount()).toBe(1);
    const rowText = await harness.getFlavorRowText(0);
    expect(rowText).toContain('nvidia.com/gpu');
    expect(rowText).toContain('56 / 64');
  });

  it('should update selected state and emit queue selection output on click', async () => {
    expect(await harness.isSelected()).toBeFalse();

    hostInstance.isSelected.set(true);
    fixture.detectChanges();
    expect(await harness.isSelected()).toBeTrue();

    await harness.click();
    fixture.detectChanges();

    expect(hostInstance.selectedQueueName()).toBe('gpu-cluster-queue');
  });
});
