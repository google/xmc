// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { ComponentHarness } from '@angular/cdk/testing';

import { DashboardGridHarness } from 'xmanager_cloud/ui/app/common/components/dashboard/dashboard_grid/dashboard_grid_harness';

/**
 * Component harness for DashboardsPage.
 */
export class DashboardsPageHarness extends ComponentHarness {
  static hostSelector = 'xmc-dashboards-page, [data-component-selector="xmc-dashboards-page"]';

  private readonly titleEl = this.locatorFor('.dashboard-title');
  private readonly gridHarness = this.locatorFor(DashboardGridHarness);
  private readonly addExperimentButton = this.locatorFor('.add-experiment-button');
  private readonly refreshButton = this.locatorFor('.refresh-button');
  private readonly shareButton = this.locatorFor('.share-button');
  private readonly notFoundEl = this.locatorForOptional('.not-found-container');

  async getTitle(): Promise<string> {
    return (await this.titleEl()).text();
  }

  async getGridHarness(): Promise<DashboardGridHarness> {
    return this.gridHarness();
  }

  async clickAddExperiment(): Promise<void> {
    return (await this.addExperimentButton()).click();
  }

  async clickRefresh(): Promise<void> {
    return (await this.refreshButton()).click();
  }

  async clickShare(): Promise<void> {
    return (await this.shareButton()).click();
  }

  async isNotFoundVisible(): Promise<boolean> {
    return (await this.notFoundEl()) !== null;
  }
}
