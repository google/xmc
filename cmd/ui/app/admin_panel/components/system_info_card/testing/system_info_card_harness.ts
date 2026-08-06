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

/** Harness for interacting with <xmc-system-info-card> in tests. */
export class SystemInfoCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-system-info-card';

  private readonly _appVersion = this.locatorForOptional('.app-version-value');
  private readonly _essSchema = this.locatorForOptional('.ess-schema-value');
  private readonly _xidSchema = this.locatorForOptional('.xid-schema-value');
  private readonly _observabilityLink = this.locatorForOptional('.observability-link');

  async getAppVersionText(): Promise<string | null> {
    const el = await this._appVersion();
    return el ? el.text() : null;
  }

  async getEssSchemaText(): Promise<string | null> {
    const el = await this._essSchema();
    return el ? el.text() : null;
  }

  async getXidSchemaText(): Promise<string | null> {
    const el = await this._xidSchema();
    return el ? el.text() : null;
  }

  async getObservabilityLinkHref(): Promise<string | null> {
    const el = await this._observabilityLink();
    return el ? el.getAttribute('href') : null;
  }
}
