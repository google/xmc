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

import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';

export type DashboardTab = 'smart_resource_finder' | 'resource_availability';
export const TAB_SMART_FINDER: DashboardTab = 'smart_resource_finder';
export const TAB_RESOURCE_AVAILABILITY: DashboardTab = 'resource_availability';

@Component({
  selector: 'xmc-rod-subnav-tabs',
  standalone: true,
  templateUrl: './subnav_tabs.ng.html',
  styleUrl: './subnav_tabs.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SubnavTabsComponent {
  readonly activeTab = input<DashboardTab>(TAB_SMART_FINDER);
  readonly activeTabChange = output<DashboardTab>();

  protected readonly TAB_SMART_FINDER = TAB_SMART_FINDER;
  protected readonly TAB_RESOURCE_AVAILABILITY = TAB_RESOURCE_AVAILABILITY;

  protected setTab(tab: DashboardTab) {
    this.activeTabChange.emit(tab);
  }
}
