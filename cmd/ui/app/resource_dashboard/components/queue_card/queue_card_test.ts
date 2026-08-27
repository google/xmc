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

import { Clipboard } from '@angular/cdk/clipboard';
import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of } from 'rxjs';

import {
  ClusterQueueSecondaryMetrics,
  QueueOverview,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';

import { QueueCardComponent } from './queue_card';
import { QueueCardHarness } from './queue_card_harness';

@Component({
  standalone: true,
  imports: [QueueCardComponent],
  template: `
    <xmc-rod-queue-card
      [queue]="queue()"
      [secondaryData]="secondaryData()"
      [isSecondaryLoading]="isSecondaryLoading()"
      [isSelected]="isSelected()"
      [activeResourceFilters]="activeResourceFilters()"
      (selectQueue)="selectedQueueName.set($event)"
    />
  `,
})
class TestHostComponent {
  readonly queue = signal<QueueOverview>({
    queueName: 'gpu-cluster-queue',
    queueCapacity: [
      {
        resourceType: 'nvidia.com/gpu',
        totalCapacity: 64,
        freeCapacity: 56,
      },
    ],
  });

  readonly secondaryData = signal<ClusterQueueSecondaryMetrics | undefined>(undefined);
  readonly isSecondaryLoading = signal<boolean>(false);
  readonly isSelected = signal<boolean>(false);
  readonly activeResourceFilters = signal<string[]>([]);
  readonly selectedQueueName = signal<string>('');
}

describe('QueueCardComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let hostInstance: TestHostComponent;
  let harness: QueueCardHarness;
  let resourceServiceSpy: jasmine.SpyObj<ResourceService>;

  beforeEach(async () => {
    resourceServiceSpy = jasmine.createSpyObj('ResourceService', ['getSubmitQueue']);
    resourceServiceSpy.getSubmitQueue.and.returnValue(of({ submitQueue: 'lq-gpu-1' }));

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [{ provide: ResourceService, useValue: resourceServiceSpy }],
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
    expect(rowText).toContain('Nominal Queue Quota');
    expect(rowText).toContain('56 / 64');
    expect(rowText).toContain('Max schedulable now (Cohort quota)');
    expect(rowText).toContain('56');
  });

  it('should render Squeezed by cohort badge and contextual banner when cohort capacity is squeezed', async () => {
    hostInstance.queue.set({
      queueName: 'squeezed-queue',
      queueCapacity: [
        {
          resourceType: 'cpu',
          totalCapacity: 100,
          freeCapacity: 60,
          freeCapacityWithBorrowing: 30,
        },
      ],
    });
    fixture.detectChanges();

    const rowText = await harness.getFlavorRowText(0);
    expect(rowText).toContain('Squeezed by cohort');
    expect(rowText).toContain('30');
    expect(rowText).toContain('60 / 100');
    expect(rowText).toContain('30 nominal quota is temporarily held by cohort peers.');
  });

  it('should render Borrowing available badge and contextual banner when cohort capacity can be borrowed', async () => {
    hostInstance.queue.set({
      queueName: 'borrowing-queue',
      queueCapacity: [
        {
          resourceType: 'cpu',
          totalCapacity: 100,
          freeCapacity: 60,
          freeCapacityWithBorrowing: 80,
        },
      ],
    });
    fixture.detectChanges();

    const rowText = await harness.getFlavorRowText(0);
    expect(rowText).toContain('Borrowing available');
    expect(rowText).toContain('80');
    expect(rowText).toContain('60 / 100');
    expect(rowText).toContain('Can borrow up to +20 beyond nominal quota from cohort.');
  });

  it('should filter flavor capacity sub-cards when activeResourceFilters are set', async () => {
    hostInstance.queue.set({
      queueName: 'multi-res-queue',
      queueCapacity: [
        { resourceType: 'nvidia.com/gpu', totalCapacity: 64, freeCapacity: 56 },
        { resourceType: 'cpu', totalCapacity: 512, freeCapacity: 448 },
      ],
    });
    fixture.detectChanges();
    expect(await harness.getFlavorRowCount()).toBe(2);

    hostInstance.activeResourceFilters.set(['cpu']);
    fixture.detectChanges();
    expect(await harness.getFlavorRowCount()).toBe(1);
    expect(await harness.getFlavorRowText(0)).toContain('cpu');
  });

  it('should restore all flavor capacity sub-cards when activeResourceFilters are cleared', async () => {
    hostInstance.queue.set({
      queueName: 'multi-res-queue',
      queueCapacity: [
        { resourceType: 'nvidia.com/gpu', totalCapacity: 64, freeCapacity: 56 },
        { resourceType: 'cpu', totalCapacity: 512, freeCapacity: 448 },
      ],
    });
    hostInstance.activeResourceFilters.set(['cpu']);
    fixture.detectChanges();
    expect(await harness.getFlavorRowCount()).toBe(1);

    hostInstance.activeResourceFilters.set([]);
    fixture.detectChanges();
    expect(await harness.getFlavorRowCount()).toBe(2);
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

  it('should render copy flag button and copy --queue flag with submit queue on click', async () => {
    expect(await harness.getCopyFlagButtonText()).toContain('Copy flag');

    const clipboard = TestBed.inject(Clipboard);
    spyOn(clipboard, 'copy').and.returnValue(true);

    await harness.clickCopyFlagButton();
    fixture.detectChanges();

    expect(resourceServiceSpy.getSubmitQueue).toHaveBeenCalledWith('gpu-cluster-queue');
    expect(clipboard.copy).toHaveBeenCalledWith('--queue=lq-gpu-1');
    expect(await harness.getCopyFlagButtonText()).toContain('Copied');
  });

  it('should render Work in Progress banner in predictability section', async () => {
    const bannerText = await harness.getWipBannerText();
    expect(bannerText).toContain('Work in Progress');
  });

  it('should display loading spinner when isSecondaryLoading is true', async () => {
    hostInstance.isSecondaryLoading.set(true);
    fixture.detectChanges();

    const spinnerText = await harness.getSpinnerContainerText();
    expect(spinnerText).not.toBeNull();
    expect(spinnerText).toContain('Fetching predictability metrics...');
  });

  it('should display fallback message when predictability data is not available', async () => {
    hostInstance.isSecondaryLoading.set(false);
    hostInstance.secondaryData.set(undefined);
    fixture.detectChanges();

    const text = await harness.getSpinnerContainerText();
    expect(text).not.toBeNull();
    expect(text).toContain('No predictability data available.');
  });

  it('should display predictability metrics when secondaryData is provided', async () => {
    hostInstance.isSecondaryLoading.set(false);
    hostInstance.secondaryData.set({
      queuePredictability: [
        {
          velocityRange: 1,
          jobVelocity: 12.5,
          resourceVelocity: {
            resourceType: 'nvidia-tesla-h100',
            velocity: 100,
          },
          pendingJobs: 3,
          pendingResourceRequests: {
            resourceType: 'nvidia-tesla-h100',
            count: 24,
          },
        },
      ],
    });
    fixture.detectChanges();

    const predText = await harness.getPredictabilitySectionText();
    expect(predText).not.toBeNull();
    expect(predText).toContain('12.5 jobs/hr');
    expect(predText).toContain('100 nvidia-tesla-h100/hr');
    expect(predText).toContain('3');
    expect(predText).toContain('24 nvidia-tesla-h100');
  });

  it('should display default units string when resourceType is missing in predictability metrics', async () => {
    hostInstance.isSecondaryLoading.set(false);
    hostInstance.secondaryData.set({
      queuePredictability: [
        {
          velocityRange: 1,
          jobVelocity: 5,
          resourceVelocity: {
            velocity: 40,
          },
          pendingJobs: 2,
          pendingResourceRequests: {
            count: 16,
          },
        },
      ],
    });
    fixture.detectChanges();

    const predText = await harness.getPredictabilitySectionText();
    expect(predText).not.toBeNull();
    expect(predText).toContain('40 units/hr');
    expect(predText).toContain('16 units');
  });
});
