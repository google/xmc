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

import { Component, signal, viewChild } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of, Subject, throwError } from 'rxjs';

import { AcceleratorSummary, GetResourceAvailabilityResponse } from 'app/protos/rod';
import { ResourceService } from 'app/service/resource_service';
import { bootstrap, cleanState, flush, getHarness, setupModule } from 'app/testing/test';

import { ResourceAvailabilityComponent } from './resource_availability';
import { ResourceAvailabilityHarness } from './resource_availability_harness';

function createSummary(overrides: Partial<AcceleratorSummary> = {}): AcceleratorSummary {
  return {
    acceleratorType: 'nvidia-tesla-h100',
    zone: 'us-central1-a',
    reservation: 'reservation-a',
    totalAccelerators: 16,
    activeAccelerators: 16,
    allocatedAccelerators: 8,
    totalNodes: 2,
    activeNodes: 2,
    clusterQueues: ['h100-queue'],
    ...overrides,
  };
}

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
  readonly availability = viewChild(ResourceAvailabilityComponent);
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
      of({ summaries: [createSummary()] }),
    );

    await errorBanner!.clickRetry();
    await flush();

    expect(await harness.getErrorBanner()).toBeNull();
    expect(await harness.hasContent()).toBeTrue();
    expect((await harness.getTableRows()).length).toBe(1);
  });

  it('should display empty state when no summaries are returned', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(of({ summaries: [] }));

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isEmpty()).toBeTrue();
  });

  it('should render per-accelerator summary cards and physical inventory table', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      of({
        summaries: [
          createSummary(),
          createSummary({
            zone: 'us-central1-b',
            reservation: '',
            totalAccelerators: 8,
            activeAccelerators: 8,
            allocatedAccelerators: 0,
            totalNodes: 1,
            activeNodes: 1,
          }),
          createSummary({
            acceleratorType: 'nvidia-tesla-a100',
            zone: 'us-central1-b',
            reservation: '',
            totalAccelerators: 8,
            activeAccelerators: 4,
            allocatedAccelerators: 4,
            totalNodes: 2,
            activeNodes: 1,
            clusterQueues: [],
          }),
        ],
      }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    const cards = await harness.getAcceleratorCards();
    expect(cards.length).toBe(2);

    // Card 0: nvidia-tesla-a100 (sorted alphabetically)
    expect(await cards[0].getTitle()).toBe('nvidia-tesla-a100');
    expect(await cards[0].getNodesText()).toBe('1 / 2 nodes');
    expect(await cards[0].getAllocatedText()).toBe('4');
    expect(await cards[0].getUnassignedText()).toBe('0');
    expect(await cards[0].getReadyText()).toBe('4 / 8');
    expect(await cards[0].getReservedText()).toBe('0');

    // Card 1: nvidia-tesla-h100
    expect(await cards[1].getTitle()).toBe('nvidia-tesla-h100');
    expect(await cards[1].getNodesText()).toBe('3 / 3 nodes');
    expect(await cards[1].getAllocatedText()).toBe('8');
    expect(await cards[1].getUnassignedText()).toBe('16');
    expect(await cards[1].getReadyText()).toBe('24 / 24');
    expect(await cards[1].getReservedText()).toBe('16');

    const rows = await harness.getTableRows();
    expect(rows.length).toBe(3);

    expect(await rows[0].getCellTexts()).toEqual([
      'nvidia-tesla-h100',
      'us-central1-a',
      'reservation-a',
      '2 / 2',
      '8',
      '8',
      '16 / 16',
      'h100-queue',
    ]);
    expect(await rows[1].getCellTexts()).toEqual([
      'nvidia-tesla-h100',
      'us-central1-b',
      'On-demand',
      '1 / 1',
      '0',
      '8',
      '8 / 8',
      'h100-queue',
    ]);
    expect(await rows[2].getCellTexts()).toEqual([
      'nvidia-tesla-a100',
      'us-central1-b',
      'On-demand',
      '1 / 2',
      '4',
      '0',
      '4 / 8',
      '—',
    ]);
  });

  it('should clamp over-allocated unassigned counts, apply proto3 fallbacks, and render multiple queue pills', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      of({
        summaries: [
          createSummary({
            activeAccelerators: 4,
            allocatedAccelerators: 8,
            clusterQueues: ['h100-queue', 'shared-queue'],
          }),
          {},
        ],
      }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    const cards = await harness.getAcceleratorCards();
    expect(cards.length).toBe(2);

    // nvidia-tesla-h100 card: active 4 < allocated 8 -> unassigned clamped to 0.
    expect(await cards[0].getTitle()).toBe('nvidia-tesla-h100');
    expect(await cards[0].getAllocatedText()).toBe('8');
    expect(await cards[0].getUnassignedText()).toBe('0');
    expect(await cards[0].getReadyText()).toBe('4 / 16');

    // Omitted proto3 fields fallback to 'unknown' and 0s.
    expect(await cards[1].getTitle()).toBe('unknown');
    expect(await cards[1].getNodesText()).toBe('0 / 0 nodes');
    expect(await cards[1].getAllocatedText()).toBe('0');
    expect(await cards[1].getUnassignedText()).toBe('0');
    expect(await cards[1].getReadyText()).toBe('0 / 0');
    expect(await cards[1].getReservedText()).toBe('0');

    const rows = await harness.getTableRows();
    expect(rows.length).toBe(2);
    expect(await rows[0].getQueuePills()).toEqual(['h100-queue', 'shared-queue']);
    expect(await rows[1].getCellTexts()).toEqual([
      'unknown',
      '—',
      'On-demand',
      '0 / 0',
      '0',
      '0',
      '0 / 0',
      '—',
    ]);
  });

  it('should refresh availability when refresh button is clicked', async () => {
    state.resourceService.getResourceAvailability.and.returnValue(
      of({ summaries: [createSummary()] }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isRefreshDisabled()).toBeFalse();
    expect((await harness.getTableRows()).length).toBe(1);

    state.resourceService.getResourceAvailability.and.returnValue(
      of({
        summaries: [createSummary(), createSummary({ zone: 'us-central1-b' })],
      }),
    );

    await harness.clickRefresh();
    await flush();

    expect(state.resourceService.getResourceAvailability).toHaveBeenCalledTimes(2);
    expect((await harness.getTableRows()).length).toBe(2);
  });

  it('should unsubscribe previous in-flight request when loadAvailability is called again', async () => {
    const first$ = new Subject<GetResourceAvailabilityResponse>();
    const second$ = new Subject<GetResourceAvailabilityResponse>();
    state.resourceService.getResourceAvailability.and.returnValues(
      first$.asObservable(),
      second$.asObservable(),
    );

    const host = bootstrap(TestHostComponent) as TestHostComponent;
    await flush();

    expect(first$.observed).toBeTrue();

    (host.availability() as unknown as { loadAvailability(): void }).loadAvailability();
    expect(first$.observed).toBeFalse();
    expect(second$.observed).toBeTrue();

    second$.next({ summaries: [] });
    second$.complete();
    await flush();

    const harness = await getHarness(ResourceAvailabilityHarness);
    expect(await harness.isLoading()).toBeFalse();
    expect(await harness.isEmpty()).toBeTrue();
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
