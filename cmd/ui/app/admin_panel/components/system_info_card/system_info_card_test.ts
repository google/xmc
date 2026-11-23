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

import { bootstrap, cleanState, flush, getHarness, setupModule } from 'app/testing/test';

import {
  SystemInfo,
  SystemInfoCardComponent,
  UNAVAILABLE_SCHEMA_VERSION,
  UNKNOWN_VALUE,
} from './system_info_card';
import { SystemInfoCardHarness } from './testing/system_info_card_harness';

@Component({
  template: `<xmc-system-info-card [systemInfo]="info()"></xmc-system-info-card>`,
  standalone: true,
  imports: [SystemInfoCardComponent],
})
class TestHostComponent {
  readonly info = signal<SystemInfo>({
    appVersion: '',
    essDbSchemaVersion: '',
    xidDbSchemaVersion: '',
  });
}

describe('SystemInfoCardComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should display version info correctly', async () => {
    const mockSystemInfo: SystemInfo = {
      appVersion: '1.0.0',
      imageTag: 'latest',
      essDbSchemaVersion: '12',
      xidDbSchemaVersion: '34',
    };

    const host = bootstrap(TestHostComponent);
    host.info.set(mockSystemInfo);
    await flush();

    const harness = await getHarness(SystemInfoCardHarness);
    expect(await harness.getAppVersionText()).toBe('1.0.0');
    expect(await harness.getImageTagText()).toBe('latest');
    expect(await harness.isImageTagPlaceholder()).toBeFalse();
    expect(await harness.getEssSchemaText()).toBe('12');
    expect(await harness.getXidSchemaText()).toBe('34');
    expect(await harness.isEssSchemaPlaceholder()).toBeFalse();
    expect(await harness.isXidSchemaPlaceholder()).toBeFalse();
  });

  it('should display - with is-placeholder class for UNAVAILABLE schema versions and unknown image tag', async () => {
    const mockSystemInfo: SystemInfo = {
      appVersion: '1.0.0',
      imageTag: UNKNOWN_VALUE,
      essDbSchemaVersion: UNAVAILABLE_SCHEMA_VERSION,
      xidDbSchemaVersion: UNAVAILABLE_SCHEMA_VERSION,
    };

    const host = bootstrap(TestHostComponent);
    host.info.set(mockSystemInfo);
    await flush();

    const harness = await getHarness(SystemInfoCardHarness);
    expect(await harness.getAppVersionText()).toBe('1.0.0');
    expect(await harness.getImageTagText()).toBe('-');
    expect(await harness.isImageTagPlaceholder()).toBeTrue();
    expect(await harness.getEssSchemaText()).toBe('-');
    expect(await harness.getXidSchemaText()).toBe('-');
    expect(await harness.isEssSchemaPlaceholder()).toBeTrue();
    expect(await harness.isXidSchemaPlaceholder()).toBeTrue();
  });

  it('should display observability link if present', async () => {
    const mockSystemInfo: SystemInfo = {
      appVersion: 'test-version',
      essDbSchemaVersion: '12',
      xidDbSchemaVersion: '34',
      observabilityUrl: 'http://test-observability',
    };

    const host = bootstrap(TestHostComponent);
    host.info.set(mockSystemInfo);
    await flush();

    const harness = await getHarness(SystemInfoCardHarness);
    expect(await harness.getObservabilityLinkHref()).toBe('http://test-observability');
  });

  it('should not display observability link if missing', async () => {
    const mockSystemInfo: SystemInfo = {
      appVersion: 'test-version',
      essDbSchemaVersion: '12',
      xidDbSchemaVersion: '34',
    };

    const host = bootstrap(TestHostComponent);
    host.info.set(mockSystemInfo);
    await flush();

    const harness = await getHarness(SystemInfoCardHarness);
    expect(await harness.getObservabilityLinkHref()).toBeNull();
  });

  it('should display - with is-placeholder class when imageTag is absent', async () => {
    const mockSystemInfo: SystemInfo = {
      appVersion: '1.0.0',
      essDbSchemaVersion: '12',
      xidDbSchemaVersion: '34',
    };

    const host = bootstrap(TestHostComponent);
    host.info.set(mockSystemInfo);
    await flush();

    const harness = await getHarness(SystemInfoCardHarness);
    expect(await harness.getImageTagText()).toBe('-');
    expect(await harness.isImageTagPlaceholder()).toBeTrue();
  });
});
