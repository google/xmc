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

/** Test harness for WipBannerComponent. */
export class WipBannerHarness extends ComponentHarness {
  static hostSelector = 'xmc-wip-banner';

  private readonly bannerTextElement = this.locatorFor('.wip-text');

  /** Gets the text content of the Work in Progress banner. */
  async getText(): Promise<string> {
    const el = await this.bannerTextElement();
    return el.text();
  }
}
