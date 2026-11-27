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
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of, Subject, throwError } from 'rxjs';

import { AcceleratorSummary, GetResourceAvailabilityResponse } from 'app/protos/rod';
import { ResourceService } from 'app/service/resource_service';
import { bootstrap, cleanState, flush, getHarness, setupModule } from 'app/testing/test';

import { ResourceAvailabilityComponent } from './resource_availability';
import { ResourceAvailabilityHarness } from './resource_availability_harness';

const BASE_SUMMARY: AcceleratorSummary = {
  acceleratorType: 'nvidia-tesla-h100',
  zone: 'us-central1-a',
  reservation: 'reservation-a',
  totalAccelerators: 16,
  activeAccelerators: 16,
  allocatedAccelerators: 8,
  totalNodes: 2,
  activeNodes: 2,
  clusterQueues: ['h100-queue'],
};

@Component({
  standalone: true,
  imports: [ResourceAvailabilityComponent],
  template: `
    @if (show()) {
      <xmc-rod-resource-availability />
    }
  `,
})
class TestHostComponent {
  readonly show = signal(true);
}

describe('ResourceAvailabilityComponent', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getResourceAvailability',
    ]);
    resourceService.getResourceAvailability.and.returnValue(of({ summaries: [] }));

    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [{ provide: ResourceService, useValue: resourceService }],
    });

    return { resourceService };
  }, beforeEach);

  it('should show loading state while fetching availability', async () => {
    const response$ = new Subject<GetResourceAvailabilityResponse>();
    state.resourceService.getResourceAvailability.and.returnValue(response$.asObservable());

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isLoading()).toBeTrue();
    expect(await harness.isRefreshDisabled()).toBeTrue();

    response$.next({ summaries: [] });
    response$.complete();
    await flush();

    expect(await harness.isLoading()).toBeFalse();
    expect(await harness.isEmpty()).toBeTrue();
  });

  it('should display error banner on failure and recover on retry', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      throwError(() => new Error('Network failure')),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    const errorBanner = await harness.getErrorBanner();
    expect(errorBanner).not.toBeNull();
    expect(await errorBanner!.getMessage()).toContain('Failed to load resource availability');

    state.resourceService.getResourceAvailability.and.returnValue(
      of({ summaries: [{ ...BASE_SUMMARY }] }),
    );

    await errorBanner!.clickRetry();
    await flush();

    expect(await harness.getErrorBanner()).toBeNull();
    expect(await harness.hasContent()).toBeTrue();
  });

  it('should display empty state when no summaries are returned', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(of({ summaries: [] }));

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isEmpty()).toBeTrue();
    expect(await harness.hasContent()).toBeFalse();
  });

  it('should display loaded summary count when summaries are returned', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      of({
        summaries: [
          { ...BASE_SUMMARY },
          {
            ...BASE_SUMMARY,
            zone: 'us-central1-b',
            reservation: '',
            totalAccelerators: 8,
            activeAccelerators: 8,
            allocatedAccelerators: 0,
            totalNodes: 1,
            activeNodes: 1,
          },
        ],
      }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.hasContent()).toBeTrue();
    expect(await harness.getContentText()).toContain('2 resource group(s) available');
  });

  it('should reload availability when the Refresh button is clicked', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      of({ summaries: [{ ...BASE_SUMMARY }] }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isRefreshDisabled()).toBeFalse();
    expect(await harness.getContentText()).toContain('1 resource group(s) available');

    state.resourceService.getResourceAvailability.and.returnValue(
      of({
        summaries: [{ ...BASE_SUMMARY }, { ...BASE_SUMMARY, zone: 'us-central1-b' }],
      }),
    );

    await harness.clickRefresh();
    await flush();

    expect(state.resourceService.getResourceAvailability).toHaveBeenCalledTimes(2);
    expect(await harness.getContentText()).toContain('2 resource group(s) available');
  });

  it('should unsubscribe in-flight request when component is destroyed', async () => {
    const response$ = new Subject<GetResourceAvailabilityResponse>();
    state.resourceService.getResourceAvailability.and.returnValue(response$.asObservable());

    const host = bootstrap(TestHostComponent) as TestHostComponent;
    await flush();

    expect(response$.observed).toBeTrue();

    host.show.set(false);
    await flush();

    expect(response$.observed).toBeFalse();
  });
});
