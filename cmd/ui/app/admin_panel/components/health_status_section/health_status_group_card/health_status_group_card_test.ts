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

import { ComponentHealth, ComponentType, HealthState } from '../health_status_section';
import { HealthStatusGroupCardComponent } from './health_status_group_card';
import { HealthStatusGroupCardHarness } from './testing/health_status_group_card_harness';

@Component({
  template: `
    <xmc-health-status-group-card
      [title]="title()"
      [icon]="icon()"
      [components]="components()"
    ></xmc-health-status-group-card>
  `,
  standalone: true,
  imports: [HealthStatusGroupCardComponent],
})
class TestHostComponent {
  readonly title = signal<string>('Core Services');
  readonly icon = signal<string>('dns');
  readonly components = signal<ComponentHealth[]>([]);
}

describe('HealthStatusGroupCardComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should render healthy status badge and items when all components are healthy', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([
      {
        name: 'actuator',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'cloud-storage',
        type: ComponentType.EXTERNAL_DEPENDENCY,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'custom-plugin',
        type: ComponentType.CUSTOM_COMPONENT,
        healthState: HealthState.HEALTHY,
      },
    ]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getTitleText()).toContain('Core Services');
    expect(await harness.getStatusText()).toBe('HEALTHY');
    expect(await harness.hasHealthyStatus()).toBeTrue();
    expect(await harness.hasDegradedStatus()).toBeFalse();
    expect(await harness.hasUnknownStatus()).toBeFalse();

    const items = await harness.getItemTexts();
    expect(items.length).toBe(3);
    expect(items[0]).toContain('actuator');
    expect(items[0]).toContain('Running');
    expect(items[1]).toContain('cloud-storage');
    expect(items[1]).toContain('Online');
    expect(items[2]).toContain('custom-plugin');
    expect(items[2]).toContain('Connected');
  });

  it('should render degraded status badge and error details when a component is unhealthy', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([
      {
        name: 'actuator',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'db-conn',
        type: ComponentType.EXTERNAL_DEPENDENCY,
        healthState: HealthState.UNHEALTHY,
        errorMessage: 'Database connection timed out after 30s',
      },
    ]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getStatusText()).toBe('DEGRADED');
    expect(await harness.hasDegradedStatus()).toBeTrue();
    expect(await harness.hasHealthyStatus()).toBeFalse();
    expect(await harness.hasUnknownStatus()).toBeFalse();

    const items = await harness.getItemTexts();
    expect(items[1]).toContain('db-conn');
    expect(items[1]).toContain('Degraded');
    expect(await harness.hasErrorDetails()).toBeTrue();
    expect(await harness.getErrorDetailsText()).toContain('Database connection timed out');
  });

  it('should render unknown status badge with neutral grey style when all components are unspecified', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([
      {
        name: 'unconfigured-service',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.UNSPECIFIED,
      },
    ]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getStatusText()).toBe('UNKNOWN');
    expect(await harness.hasUnknownStatus()).toBeTrue();
    expect(await harness.hasHealthyStatus()).toBeFalse();
    expect(await harness.hasDegradedStatus()).toBeFalse();

    const items = await harness.getItemTexts();
    expect(items.length).toBe(1);
    expect(items[0]).toContain('unconfigured-service');
    expect(items[0]).toContain('Unknown');
  });

  it('should render unknown status badge when components array is empty', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getStatusText()).toBe('UNKNOWN');
    expect(await harness.hasUnknownStatus()).toBeTrue();
    expect(await harness.hasHealthyStatus()).toBeFalse();
    expect(await harness.hasDegradedStatus()).toBeFalse();
  });

  it('should render unknown badge when group has both healthy and unspecified components', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([
      {
        name: 'actuator',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.HEALTHY,
      },
      {
        name: 'optional-feature',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.UNSPECIFIED,
      },
    ]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getStatusText()).toBe('UNKNOWN');
    expect(await harness.hasUnknownStatus()).toBeTrue();
    expect(await harness.hasHealthyStatus()).toBeFalse();
    expect(await harness.hasDegradedStatus()).toBeFalse();

    const items = await harness.getItemTexts();
    expect(items.length).toBe(2);
    expect(items[0]).toContain('Running');
    expect(items[1]).toContain('Unknown');
  });

  it('should render degraded badge when group has both unhealthy and unspecified components', async () => {
    const host = bootstrap(TestHostComponent);
    host.components.set([
      {
        name: 'failed-service',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.UNHEALTHY,
        errorMessage: 'CrashLoopBackOff',
      },
      {
        name: 'optional-feature',
        type: ComponentType.CORE_SERVICE,
        healthState: HealthState.UNSPECIFIED,
      },
    ]);
    await flush();

    const harness = await getHarness(HealthStatusGroupCardHarness);
    expect(await harness.getStatusText()).toBe('DEGRADED');
    expect(await harness.hasDegradedStatus()).toBeTrue();
  });
});
