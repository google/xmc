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
import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';

import { WipBannerHarness } from '../common/components/wip_banner/wip_banner_harness';
import { QueueJobsTableHarness } from '../side_panel/contents/queue_jobs_table/queue_jobs_table_harness';
import { QueueListHarness } from './components/queue_list/queue_list_harness';
import { SmartResourceFinderHarness } from './components/smart_resource_finder/smart_resource_finder_harness';
import { RodSubnavTabsHarness } from './components/subnav_tabs/subnav_tabs_harness';

/** Harness for interacting with <xmc-resource-dashboard> in tests. */
export class ResourceDashboardHarness extends ComponentHarness {
  static hostSelector = 'xmc-resource-dashboard';

  private readonly splitContainer = this.locatorForOptional('.rod-split-container');
  private readonly mainContent = this.locatorForOptional('.rod-main-content');
  private readonly resizeDivider = this.locatorForOptional('.rod-resize-divider');
  private readonly requiredResizeDivider = this.locatorFor('.rod-resize-divider');
  private readonly jobsSidePane = this.locatorForOptional('.jobs-side-pane');

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

  /** Gets the queue jobs table harness if present. */
  async getQueueJobsTable(): Promise<QueueJobsTableHarness | null> {
    return this.locatorForOptional(QueueJobsTableHarness)();
  }

  /** Gets the WIP banner harness if present. */
  async getWipBanner(): Promise<WipBannerHarness | null> {
    return this.locatorForOptional(WipBannerHarness)();
  }

  /** Returns true when the vertical resize divider is rendered. */
  async hasResizeDivider(): Promise<boolean> {
    return (await this.resizeDivider()) !== null;
  }

  /** Gets accessibility and tooltip attributes of the vertical resize divider. */
  async getResizeDividerAttributes(): Promise<{
    role: string | null;
    ariaLabel: string | null;
    title: string | null;
    ariaValueNow: string | null;
  } | null> {
    const divider = await this.resizeDivider();
    if (!divider) {
      return null;
    }
    return {
      role: await divider.getAttribute('role'),
      ariaLabel: await divider.getAttribute('aria-label'),
      title: await divider.getAttribute('title'),
      ariaValueNow: await divider.getAttribute('aria-valuenow'),
    };
  }

  /** Gets the inline style string of the left queue-list pane if present. */
  async getLeftPaneStyle(): Promise<string | null> {
    const pane = await this.mainContent();
    return pane ? pane.getAttribute('style') : null;
  }

  /** Gets the inline style string of the right jobs side pane if present. */
  async getRightPaneStyle(): Promise<string | null> {
    const pane = await this.jobsSidePane();
    return pane ? pane.getAttribute('style') : null;
  }

  /** Returns true when the right jobs pane has full-width mode active. */
  async isRightPaneFullWidth(): Promise<boolean> {
    const pane = await this.jobsSidePane();
    return pane ? pane.hasClass('full-width') : false;
  }

  /** Gets the native split container element for geometry stubbing in unit tests. */
  async getSplitContainerNativeElement(): Promise<Element | null> {
    const container = await this.splitContainer();
    return container ? TestbedHarnessEnvironment.getNativeElement(container) : null;
  }

  /** Starts a resize drag gesture on the vertical divider. */
  async startResizeDrag(clientX = 500): Promise<void> {
    await (await this.requiredResizeDivider()).dispatchEvent('mousedown', { clientX });
  }

  /** Double-clicks the vertical resize divider to reset column widths. */
  async doubleClickResizeDivider(): Promise<void> {
    await (await this.requiredResizeDivider()).dispatchEvent('dblclick');
  }

  /** Dispatches a keyboard event on the vertical resize divider. */
  async pressResizeDividerKey(key: string): Promise<void> {
    await (await this.requiredResizeDivider()).dispatchEvent('keydown', { key });
  }
}
