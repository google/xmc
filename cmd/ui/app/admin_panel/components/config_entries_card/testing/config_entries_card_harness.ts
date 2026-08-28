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

/** Harness for interacting with config rows in tests. */
export class ConfigRowHarness extends ComponentHarness {
  static hostSelector = '.config-row';

  private readonly _key = this.locatorFor('.cell-key');
  private readonly _value = this.locatorFor('.cell-value');

  async getKeyText(): Promise<string> {
    return (await this._key()).text();
  }

  async getValueText(): Promise<string> {
    return (await this._value()).text();
  }
}

/** Harness for interacting with <xmc-config-entries-card> in tests. */
export class ConfigEntriesCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-config-entries-card';

  private readonly _searchInput = this.locatorForOptional('.filter-input');
  private readonly _groupHeaderRows = this.locatorForAll('.group-header-row');
  private readonly _groupTitles = this.locatorForAll('.group-display-name');
  private readonly _configRows = this.locatorForAll(ConfigRowHarness);
  private readonly _noResults = this.locatorForOptional('.no-results-cell');
  private readonly _errorBanner = this.locatorForOptional('.error-banner');
  private readonly _retryButton = this.locatorForOptional('.retry-button');
  private readonly _spinner = this.locatorForOptional('mat-spinner');

  async hasSpinner(): Promise<boolean> {
    const el = await this._spinner();
    return el !== null;
  }

  async enterSearchTerm(term: string): Promise<void> {
    const input = await this._searchInput();
    if (!input) {
      throw new Error('Search input not found on ConfigEntriesCard');
    }
    await input.clear();
    if (term) {
      await input.sendKeys(term);
    }
    // Dispatch input event to trigger signal update
    await input.dispatchEvent('input');
  }

  async getGroupDisplayNames(): Promise<string[]> {
    const titles = await this._groupTitles();
    return Promise.all(titles.map((t) => t.text()));
  }

  async toggleGroup(index: number): Promise<void> {
    const rows = await this._groupHeaderRows();
    if (rows[index]) {
      await rows[index].click();
    }
  }

  async isGroupCollapsed(index: number): Promise<boolean> {
    const rows = await this._groupHeaderRows();
    if (!rows[index]) return false;
    const expanded = await rows[index].getAttribute('aria-expanded');
    return expanded === 'false';
  }

  async getConfigEntries(): Promise<Array<{ key: string; value: string }>> {
    const rows = await this._configRows();
    return Promise.all(
      rows.map(async (row) => {
        return {
          key: (await row.getKeyText()).trim(),
          value: (await row.getValueText()).trim(),
        };
      }),
    );
  }

  async getNoResultsText(): Promise<string | null> {
    const el = await this._noResults();
    return el ? el.text() : null;
  }

  async getErrorBannerText(): Promise<string | null> {
    const el = await this._errorBanner();
    return el ? el.text() : null;
  }

  async hasErrorBanner(): Promise<boolean> {
    const el = await this._errorBanner();
    return el !== null;
  }

  async hasRetryButton(): Promise<boolean> {
    const el = await this._retryButton();
    return el !== null;
  }

  async clickRetry(): Promise<void> {
    const button = await this._retryButton();
    if (!button) {
      throw new Error('Retry button not found in error banner');
    }
    await button.click();
  }
}
