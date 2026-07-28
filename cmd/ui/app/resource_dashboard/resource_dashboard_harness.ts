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

import { ComponentHarness } from '@angular/cdk/testing';

import { QueueListHarness } from './components/queue_list/queue_list_harness';
import { SmartResourceFinderHarness } from './components/smart_resource_finder/smart_resource_finder_harness';
import { RodSubnavTabsHarness } from './components/subnav_tabs/subnav_tabs_harness';

/** Harness for interacting with <xmc-resource-dashboard> in tests. */
export class ResourceDashboardHarness extends ComponentHarness {
  static hostSelector = 'xmc-resource-dashboard';

  /** Gets the subnav tabs harness. */
  async getSubnavTabs(): Promise<RodSubnavTabsHarness> {
    return this.locatorFor(RodSubnavTabsHarness)();
  }

  /** Gets the smart resource finder harness if present. */
  async getSmartResourceFinder(): Promise<SmartResourceFinderHarness | null> {
    return this.locatorForOptional(SmartResourceFinderHarness)();
  }

  /** Gets the queue list harness if present. */
  async getQueueList(): Promise<QueueListHarness | null> {
    return this.locatorForOptional(QueueListHarness)();
  }
}
