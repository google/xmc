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

import { ConfigEntriesCardHarness } from '../components/config_entries_card/testing/config_entries_card_harness';
import { HealthStatusSectionHarness } from '../components/health_status_section/testing/health_status_section_harness';
import { SystemInfoCardHarness } from '../components/system_info_card/testing/system_info_card_harness';

/** Harness for interacting with <xmc-admin-panel> in tests. */
export class AdminPanelHarness extends ComponentHarness {
  static hostSelector = 'xmc-admin-panel, [data-component-selector="xmc-admin-panel"]';

  private readonly _timestampBadge = this.locatorForOptional('.timestamp-badge');
  private readonly _errorContainer = this.locatorForOptional('.error-container');

  async getTimestampText(): Promise<string | null> {
    const el = await this._timestampBadge();
    return el ? el.text() : null;
  }

  async getErrorText(): Promise<string | null> {
    const el = await this._errorContainer();
    return el ? el.text() : null;
  }

  async getSystemInfoCard(): Promise<SystemInfoCardHarness | null> {
    return this.locatorForOptional(SystemInfoCardHarness)();
  }

  async getHealthStatusSection(): Promise<HealthStatusSectionHarness | null> {
    return this.locatorForOptional(HealthStatusSectionHarness)();
  }

  async getConfigEntriesCard(): Promise<ConfigEntriesCardHarness | null> {
    return this.locatorForOptional(ConfigEntriesCardHarness)();
  }
}
