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
      (refresh)="onRefresh()"
    ></xmc-health-status-section>
  `,
  standalone: true,
  imports: [HealthStatusSectionComponent],
})
class TestHostComponent {
  readonly components = signal<ComponentHealth[]>([]);
  readonly overallStatus = signal<string>(HealthState.UNSPECIFIED);
  readonly lastChecked = signal<string>('');

  refreshCalled = false;

  onRefresh() {
    this.refreshCalled = true;
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
    expect(coreInstancesCardItems[0]).toContain('HEALTHY');
    expect(dependenciesCardItems.length).toBe(1);
    expect(dependenciesCardItems[0]).toContain('db-conn');
    expect(dependenciesCardItems[0]).toContain('DEGRADED');
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
});
