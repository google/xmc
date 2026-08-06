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

import { provideHttpClient } from '@angular/common/http';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of, throwError } from 'rxjs';

import { HealthService } from '../service/health_service';
import { SnackbarService } from '../service/snackbar_service';
import { MockHealthService } from '../service/testing/mock_health_service';
import { bootstrap, cleanState, flush, get, getHarness, setupModule } from '../testing/test';
import { AdminPanelPage } from './admin_panel';
import { AdminPanelHarness } from './testing/admin_panel_harness';

describe('AdminPanelPage Component', () => {
  cleanState(() => {
    setupModule({
      imports: [AdminPanelPage, NoopAnimationsModule],
      providers: [provideHttpClient(), { provide: HealthService, useClass: MockHealthService }],
    });
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(AdminPanelPage);

    expect(comp).toBeTruthy();
  });

  it('should display correct page header and version info', async () => {
    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const expectedTime = new Date(1717804800 * 1000).toLocaleTimeString();

    expect(await harness.getTimestampText()).toContain(expectedTime);

    const systemInfoHarness = await harness.getSystemInfoCard();
    expect(systemInfoHarness).not.toBeNull();
    expect(await systemInfoHarness!.getAppVersionText()).toBe('mock-app-version-1.0.0');
    expect(await systemInfoHarness!.getEssSchemaText()).toBe('6');
    expect(await systemInfoHarness!.getXidSchemaText()).toBe('1');
  });

  it('should display services health card', async () => {
    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const healthStatusSectionHarness = await harness.getHealthStatusSection();
    expect(healthStatusSectionHarness).not.toBeNull();
    expect(await healthStatusSectionHarness!.getOverallStatusText()).toBe('HEALTHY');
  });

  it('should display system observability link if observabilityUrl is present', async () => {
    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const systemInfoHarness = await harness.getSystemInfoCard();
    expect(systemInfoHarness).not.toBeNull();
    expect(await systemInfoHarness!.getObservabilityLinkHref()).toBe(
      'http://mock-observability-url',
    );
  });

  it('should not display system observability link if observabilityUrl is missing', async () => {
    const healthService = get(HealthService);
    spyOn(healthService, 'getHealthStatus').and.returnValue(
      of({
        healthState: 'HEALTH_STATE_HEALTHY',
        systemInfo: {
          appVersion: 'mock-app-version-1.0.0',
          essDbSchemaVersion: '6',
          xidDbSchemaVersion: '1',
        },
        healthCheckTime: { seconds: 1717804800, nanos: 0 },
      } as any),
    );

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const systemInfoHarness = await harness.getSystemInfoCard();
    expect(systemInfoHarness).not.toBeNull();
    expect(await systemInfoHarness!.getObservabilityLinkHref()).toBeNull();
  });

  it('should display error message if health status fails to load', async () => {
    const healthService = get(HealthService);
    spyOn(healthService, 'getHealthStatus').and.returnValue(
      throwError(() => new Error('Mock error')),
    );

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    expect(await harness.getErrorText()).toContain('Failed to load health status');
  });

  it('should display toast and preserve old health status if subsequent refresh fails', async () => {
    const healthService = get(HealthService);
    const snackbarService = get(SnackbarService);
    const openSpy = spyOn(snackbarService, 'open');
    const firstStatus = {
      healthState: 'HEALTH_STATE_HEALTHY',
      systemInfo: {
        appVersion: 'mock-app-version-1.0.0',
        essDbSchemaVersion: '6',
        xidDbSchemaVersion: '1',
      },
      components: [],
      healthCheckTime: { seconds: 1717804800, nanos: 0 },
    };
    spyOn(healthService, 'getHealthStatus').and.returnValues(
      of(firstStatus as any),
      throwError(() => new Error('Refresh error')),
    );

    bootstrap(AdminPanelPage);
    await flush();
    const harness = await getHarness(AdminPanelHarness);
    const healthStatusSectionHarness = await harness.getHealthStatusSection();
    await healthStatusSectionHarness!.clickRefresh();
    await flush();
    const preservedSection = await harness.getHealthStatusSection();

    expect(openSpy).toHaveBeenCalledWith('Failed to refresh health status.');
    expect(await harness.getErrorText()).toBeNull();
    expect(preservedSection).not.toBeNull();
    expect(await preservedSection!.getOverallStatusText()).toBe('HEALTHY');
  });
});
