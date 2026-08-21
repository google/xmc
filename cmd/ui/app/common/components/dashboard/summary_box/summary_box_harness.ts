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

/**
 * Component harness for SummaryBoxComponent.
 */
export class SummaryBoxHarness extends ComponentHarness {
  static hostSelector = 'xmc-summary-box';

  private readonly titleEl = this.locatorFor('.summary-title');
  private readonly valueEl = this.locatorFor('.summary-value');
  private readonly deltaEl = this.locatorForOptional('.summary-delta');

  async getTitle(): Promise<string> {
    return (await this.titleEl()).text();
  }

  async getValue(): Promise<string> {
    return (await this.valueEl()).text();
  }

  async hasDelta(): Promise<boolean> {
    return (await this.deltaEl()) !== null;
  }

  async getDeltaText(): Promise<string> {
    const el = await this.deltaEl();
    return el ? el.text() : '';
  }

  async isDeltaImproved(): Promise<boolean> {
    const el = await this.deltaEl();
    if (!el) return false;
    return el.hasClass('improved');
  }

  async isDeltaWorse(): Promise<boolean> {
    const el = await this.deltaEl();
    if (!el) return false;
    return el.hasClass('worse');
  }
}
