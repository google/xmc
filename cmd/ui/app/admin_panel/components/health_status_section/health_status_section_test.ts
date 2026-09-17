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
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
} from 'xmanager_cloud/ui/app/testing/test';

import {
  ComponentHealth,
  ComponentType,
  HealthState,
  HealthStatusSectionComponent,
} from './health_status_section';
import { HealthStatusSectionHarness } from './testing/health_status_section_harness';

@Component({
  template: `
    <xmc-health-status-section
      [components]="components()"
      [overallStatus]="overallStatus()"
      [lastChecked]="lastChecked()"
      [loading]="loading()"
      [isSchedulingPaused]="isSchedulingPaused()"
      [isPauseSchedulingEnabled]="isPauseSchedulingEnabled()"
      (refresh)="onRefresh()"
      (toggleSchedulingPause)="onToggleSchedulingPause($event)"
    ></xmc-health-status-section>
  `,
  standalone: true,
  imports: [HealthStatusSectionComponent],
})
class TestHostComponent {
  readonly components = signal<ComponentHealth[]>([]);
  readonly overallStatus = signal<string>(HealthState.UNSPECIFIED);
  readonly lastChecked = signal<string>('');
  readonly loading = signal<boolean>(false);
  readonly isSchedulingPaused = signal<boolean>(false);
  readonly isPauseSchedulingEnabled = signal<boolean>(true);

  refreshCalled = false;
  lastToggledState: boolean | null = null;

  onRefresh() {
    this.refreshCalled = true;
  }

  onToggleSchedulingPause(state: boolean) {
    this.lastToggledState = state;
  }
}

describe('HealthStatusSectionComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should render core operations and dependencies correctly', async () => {
    const mockComponents: ComponentHealth[] = [
      {
        name: 'actuator',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'db-conn',
        type: ComponentType.EXTERNAL_DEPENDENCY,
        healthState: HealthState.UNHEALTHY,
        errorMessage: 'Connection lost',
      },
    ];
    const host = bootstrap(TestHostComponent);

    host.components.set(mockComponents);
    host.overallStatus.set(HealthState.UNHEALTHY);
    await flush();
    const harness = await getHarness(HealthStatusSectionHarness);
    const cards = await harness.getGroupCards();
    const coreInstancesCardItems = await cards[0].getItemTexts();
    const dependenciesCardItems = await cards[1].getItemTexts();

    expect(await harness.getTitleText()).toContain('XMC Services Health');
    expect(await harness.getOverallStatusText()).toContain('DEGRADED');
    expect(cards.length).toBe(2);
    expect(await cards[0].getTitleText()).toContain('Core XMC Instances');
    expect(await cards[1].getTitleText()).toContain('GCP Hosted Services');
    expect(coreInstancesCardItems.length).toBe(1);
    expect(coreInstancesCardItems[0]).toContain('actuator');
    expect(coreInstancesCardItems[0]).toContain('Running');
    expect(dependenciesCardItems.length).toBe(1);
    expect(dependenciesCardItems[0]).toContain('db-conn');
    expect(dependenciesCardItems[0]).toContain('Degraded');
    expect(await cards[1].hasErrorDetails()).toBeTrue();
    expect(await cards[1].getErrorDetailsText()).toContain('View Error Details');
  });

  it('should emit refresh event on refresh button click', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    const harness = await getHarness(HealthStatusSectionHarness);

    await harness.clickRefresh();
    await flush();

    expect(host.refreshCalled).toBeTrue();
  });

  it('should disable refresh button and show loading text when loading is true', async () => {
    const host = bootstrap(TestHostComponent);
    host.loading.set(true);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    expect(await harness.isRefreshDisabled()).toBeTrue();
    expect(await harness.getRefreshButtonText()).toContain('Refreshing...');
  });

  it('should render group as UNKNOWN when at least one component is unspecified', async () => {
    const mockComponents: ComponentHealth[] = [
      {
        name: 'actuator',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'log-monitor',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.UNSPECIFIED,
      },
    ];
    const host = bootstrap(TestHostComponent);

    host.components.set(mockComponents);
    host.overallStatus.set(HealthState.HEALTHY);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    const cards = await harness.getGroupCards();
    const items = await cards[0].getItemTexts();

    expect(cards.length).toBe(1);
    expect(await cards[0].getStatusText()).toBe('UNKNOWN');
    expect(items.length).toBe(2);
    expect(items[0]).toContain('actuator');
    expect(items[0]).toContain('Running');
    expect(items[1]).toContain('log-monitor');
    expect(items[1]).toContain('Unknown');
  });

  it('should render pause scheduling toggle in active state', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(false);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    expect(await harness.hasSchedulingPauseCard()).toBeTrue();
    expect(await harness.isSchedulingPauseCardPaused()).toBeFalse();
    expect(await harness.isPauseToggleChecked()).toBeFalse();
    expect(await harness.isPauseToggleDisabled()).toBeFalse();
  });

  it('should render pause scheduling toggle in paused state', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(true);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    expect(await harness.hasSchedulingPauseCard()).toBeTrue();
    expect(await harness.isSchedulingPauseCardPaused()).toBeTrue();
    expect(await harness.isPauseToggleChecked()).toBeTrue();
  });

  it('should not render pause scheduling toggle when isPauseSchedulingEnabled is false', async () => {
    const host = bootstrap(TestHostComponent);
    host.isPauseSchedulingEnabled.set(false);
    host.isSchedulingPaused.set(true);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    expect(await harness.hasSchedulingPauseCard()).toBeFalse();
  });

  it('should open confirmation modal dialog when toggle is clicked with pause warning', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(false);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    expect(await harness.hasConfirmationModal()).toBeFalse();

    await harness.togglePause();
    await flush();

    expect(await harness.hasConfirmationModal()).toBeTrue();
    const msg = await harness.getModalMessageText();
    expect(msg).toContain('Are you sure you want to pause all scheduling?');
    expect(msg).toContain('stop the actuator from interacting with the control plane');
  });

  it('should open confirmation modal dialog with resume warning when toggling from paused', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(true);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    await harness.togglePause();
    await flush();

    expect(await harness.hasConfirmationModal()).toBeTrue();
    const msg = await harness.getModalMessageText();
    expect(msg).toContain('Are you sure you want to resume all scheduling?');
    expect(msg).toContain('resume normal scheduling operations');
  });

  it('should close modal without emitting event when Cancel is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(false);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    await harness.togglePause();
    await flush();
    expect(await harness.hasConfirmationModal()).toBeTrue();

    await harness.clickModalCancel();
    await flush();

    expect(await harness.hasConfirmationModal()).toBeFalse();
    expect(host.lastToggledState).toBeNull();
  });

  it('should emit toggleSchedulingPause and close modal when Confirm is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(false);
    await flush();

    const harness = await getHarness(HealthStatusSectionHarness);
    await harness.togglePause();
    await flush();

    await harness.clickModalConfirm();
    await flush();

    expect(await harness.hasConfirmationModal()).toBeFalse();
    expect(host.lastToggledState).toBeTrue();
  });
});
