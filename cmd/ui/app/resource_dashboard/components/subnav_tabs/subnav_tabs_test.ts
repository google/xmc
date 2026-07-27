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

import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import {
  DashboardTab,
  SubnavTabsComponent,
  TAB_RESOURCE_AVAILABILITY,
  TAB_SMART_FINDER,
} from './subnav_tabs';
import { RodSubnavTabsHarness } from './subnav_tabs_harness';

@Component({
  standalone: true,
  imports: [SubnavTabsComponent],
  template: `
    <xmc-rod-subnav-tabs [activeTab]="activeTab()" (activeTabChange)="activeTab.set($event)" />
  `,
})
class TestHostComponent {
  readonly activeTab = signal<DashboardTab>(TAB_SMART_FINDER);
}

describe('SubnavTabsComponent', () => {
  beforeAll(setupObservableMatchers);

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
    });
    return {};
  }, beforeEach);

  it('should render tabs and emit change on click', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(RodSubnavTabsHarness);
    const tabTexts = await harness.getTabTexts();
    expect(tabTexts.length).toBe(2);
    expect(await harness.isTabActive(0)).toBeTrue();
    expect(await harness.isTabActive(1)).toBeFalse();

    await harness.clickTab(1);
    await flush();

    expect(host.activeTab()).toBe(TAB_RESOURCE_AVAILABILITY);
    expect(await harness.isTabActive(0)).toBeFalse();
    expect(await harness.isTabActive(1)).toBeTrue();
  });
});
