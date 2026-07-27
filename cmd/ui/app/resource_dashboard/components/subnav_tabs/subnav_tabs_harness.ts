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

/** Harness for interacting with <xmc-rod-subnav-tabs> in tests. */
export class RodSubnavTabsHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-subnav-tabs';

  private readonly tabs = this.locatorForAll('button.subnav-tab');

  /** Gets the text of all subnav tabs. */
  async getTabTexts(): Promise<string[]> {
    const tabEls = await this.tabs();
    return Promise.all(tabEls.map((t) => t.text()));
  }

  /** Clicks the tab at the given index (0 for Smart finder, 1 for Resource availability). */
  async clickTab(index: number): Promise<void> {
    const tabEls = await this.tabs();
    if (!tabEls[index]) {
      throw new Error(`Tab index ${index} out of bounds.`);
    }
    await tabEls[index].click();
  }

  /** Checks if the tab at the given index is active. */
  async isTabActive(index: number): Promise<boolean> {
    const tabEls = await this.tabs();
    if (!tabEls[index]) {
      throw new Error(`Tab index ${index} out of bounds.`);
    }
    return tabEls[index].hasClass('active');
  }
}
