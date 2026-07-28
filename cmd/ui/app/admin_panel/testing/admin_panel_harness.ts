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

/** Harness for interacting with <xmc-admin-panel> in tests. */
export class AdminPanelHarness extends ComponentHarness {
  static hostSelector = 'xmc-admin-panel, [data-component-selector="xmc-admin-panel"]';

  private readonly _healthStatus = this.locatorForOptional('.status-section pre.raw-json');
  private readonly _healthConfigs = this.locatorForOptional('.config-section pre.raw-json');

  async getHealthStatusText(): Promise<string | null> {
    const el = await this._healthStatus();
    return el ? el.text() : null;
  }

  async getHealthConfigsText(): Promise<string | null> {
    const el = await this._healthConfigs();
    return el ? el.text() : null;
  }
}
