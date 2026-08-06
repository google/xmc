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

import { HealthStatusGroupCardHarness } from '../health_status_group_card/testing/health_status_group_card_harness';

/** Harness for interacting with <xmc-health-status-section> in tests. */
export class HealthStatusSectionHarness extends ComponentHarness {
  static hostSelector = 'xmc-health-status-section';

  private readonly _title = this.locatorFor('.card-title');
  private readonly _refreshBtn = this.locatorFor('.refresh-button');
  private readonly _overallBadge = this.locatorFor('.status-badge');

  async getTitleText(): Promise<string> {
    const el = await this._title();
    return el.text();
  }

  async clickRefresh(): Promise<void> {
    const btn = await this._refreshBtn();
    return btn.click();
  }

  async getOverallStatusText(): Promise<string> {
    const badge = await this._overallBadge();
    return badge.text();
  }

  async getGroupCards(): Promise<HealthStatusGroupCardHarness[]> {
    return this.locatorForAll(HealthStatusGroupCardHarness)();
  }
}
