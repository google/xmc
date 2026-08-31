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
import { of, Subject, throwError } from 'rxjs';

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

  it('should display configuration entries card with correct values', async () => {
    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const configEntriesCardHarness = await harness.getConfigEntriesCard();
    expect(configEntriesCardHarness).not.toBeNull();
    const entries = await configEntriesCardHarness!.getConfigEntries();
    expect(await configEntriesCardHarness!.getGroupDisplayNames()).toEqual([
      'Health Service Config',
    ]);
    expect(entries.length).toBe(3);
    expect(entries[0]).toEqual({ key: 'Port', value: '8080' });
    expect(entries[1]).toEqual({ key: 'GRPCPort', value: '50051' });
    expect(entries[2]).toEqual({ key: 'ProjectID', value: 'mock-project' });
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
    expect(await harness.getErrorText()).toContain('Failed to load admin panel');
    expect(await harness.hasPageErrorBanner()).toBeTrue();
  });

  it('should retry loading health status when clicking retry on page error banner', async () => {
    const healthService = get(HealthService);
    let attempts = 0;
    const mockStatus = {
      healthState: 'HEALTH_STATE_HEALTHY',
      systemInfo: {
        appVersion: 'mock-app-version-1.0.0',
        essDbSchemaVersion: '6',
        xidDbSchemaVersion: '1',
      },
      components: [],
      healthCheckTime: { seconds: 1717804800, nanos: 0 },
    };
    spyOn(healthService, 'getHealthStatus').and.callFake(() => {
      attempts++;
      if (attempts === 1) {
        return throwError(() => new Error('Initial load failed'));
      }
      return of(mockStatus as any);
    });

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    expect(await harness.hasPageErrorBanner()).toBeTrue();
    expect(await harness.getErrorText()).toContain('Failed to load admin panel.');

    await harness.clickPageErrorRetry();
    await flush();

    expect(attempts).toBe(2);
    expect(await harness.hasPageErrorBanner()).toBeFalse();
    expect(await harness.getHealthStatusSection()).not.toBeNull();
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

  it('should display progress spinner while loading', async () => {
    const healthService = get(HealthService);
    // Never emit to keep isLoading true
    spyOn(healthService, 'getHealthStatus').and.returnValue(new Subject<any>());

    bootstrap(AdminPanelPage);
    // Flush microtasks without completing observable
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    expect(await harness.hasSpinner()).toBeTrue();
  });

  it('should display error banner in config entries card if health configs fail to load', async () => {
    const healthService = get(HealthService);
    spyOn(healthService, 'getHealthConfigs').and.returnValue(
      throwError(() => new Error('Configs load failed')),
    );

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const configEntriesCardHarness = await harness.getConfigEntriesCard();
    expect(configEntriesCardHarness).not.toBeNull();
    expect(await configEntriesCardHarness!.hasErrorBanner()).toBeTrue();
    expect(await configEntriesCardHarness!.getErrorBannerText()).toContain(
      'Failed to load configuration entries.',
    );
  });

  it('should retry fetching health configs when clicking retry in config entries error banner', async () => {
    const healthService = get(HealthService);
    let attempts = 0;
    spyOn(healthService, 'getHealthConfigs').and.callFake(() => {
      attempts++;
      if (attempts === 1) {
        return throwError(() => new Error('First attempt failed'));
      }
      return of({
        configGroups: [
          {
            displayName: 'Retried Config Group',
            configs: [{ key: 'retried.key', value: 'retried.value' }],
          },
        ],
      });
    });

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const cardHarness = await harness.getConfigEntriesCard();
    expect(cardHarness).not.toBeNull();
    expect(await cardHarness!.hasErrorBanner()).toBeTrue();
    expect(await cardHarness!.hasRetryButton()).toBeTrue();

    await cardHarness!.clickRetry();
    await flush();

    expect(attempts).toBe(2);
    expect(await cardHarness!.hasErrorBanner()).toBeFalse();
    expect(await cardHarness!.getGroupDisplayNames()).toEqual(['Retried Config Group']);
  });

  it('should display toast and preserve old configs if subsequent refresh fails', async () => {
    const healthService = get(HealthService);
    const snackbarService = get(SnackbarService);
    const openSpy = spyOn(snackbarService, 'open');
    const firstConfigs = {
      configGroups: [
        {
          displayName: 'Initial Group',
          configs: [{ key: 'init.key', value: 'init.val' }],
        },
      ],
    };
    spyOn(healthService, 'getHealthConfigs').and.returnValues(
      of(firstConfigs as any),
      throwError(() => new Error('Refresh configs error')),
    );

    bootstrap(AdminPanelPage);
    await flush();

    const harness = await getHarness(AdminPanelHarness);
    const cardHarness = await harness.getConfigEntriesCard();
    expect(cardHarness).not.toBeNull();
    expect(await cardHarness!.getGroupDisplayNames()).toEqual(['Initial Group']);

    const healthStatusSectionHarness = await harness.getHealthStatusSection();
    await healthStatusSectionHarness!.clickRefresh();
    await flush();

    expect(openSpy).toHaveBeenCalledWith('Failed to refresh configuration entries.');
    expect(await cardHarness!.hasErrorBanner()).toBeFalse();
    expect(await cardHarness!.getGroupDisplayNames()).toEqual(['Initial Group']);
  });
});
