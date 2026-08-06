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

/** Harness for interacting with <xmc-health-status-group-card> in tests. */
export class HealthStatusGroupCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-health-status-group-card';

  private readonly _title = this.locatorFor('.group-title');
  private readonly _statusBadge = this.locatorFor('.group-status-badge');
  private readonly _items = this.locatorForAll('.item-row');
  private readonly _errorDetails = this.locatorForOptional('.item-error-details');

  async getTitleText(): Promise<string> {
    const el = await this._title();
    return el.text();
  }

  async getStatusText(): Promise<string> {
    const el = await this._statusBadge();
    return el.text();
  }

  async getItemTexts(): Promise<string[]> {
    const items = await this._items();
    return Promise.all(items.map((item) => item.text()));
  }

  async hasErrorDetails(): Promise<boolean> {
    const el = await this._errorDetails();
    return el !== null;
  }

  async getErrorDetailsText(): Promise<string | null> {
    const el = await this._errorDetails();
    return el ? el.text() : null;
  }
}
