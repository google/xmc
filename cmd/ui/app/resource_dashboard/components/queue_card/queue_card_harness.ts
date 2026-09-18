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

/** Harness for interacting with <xmc-rod-queue-card> in tests. */
export class QueueCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-queue-card';

  private readonly cardElement = this.locatorFor('.queue-card');
  private readonly queueNameElement = this.locatorFor('.queue-name');
  private readonly statusBadgeElement = this.locatorForOptional('.status-badge');
  private readonly tableRows = this.locatorForAll('.table-row');
  private readonly copyFlagButtonElement = this.locatorForOptional('.copy-flag-btn');

  /** Gets the queue name title text. */
  async getQueueName(): Promise<string> {
    const el = await this.queueNameElement();
    return el.text();
  }

  /** Returns true if the card has the 'selected' CSS class. */
  async isSelected(): Promise<boolean> {
    const card = await this.cardElement();
    return card.hasClass('selected');
  }

  /** Gets the status badge label text if present. */
  async getStatusBadgeText(): Promise<string | null> {
    const badge = await this.statusBadgeElement();
    return badge ? badge.text() : null;
  }

  /** Returns true if the status badge is healthy. */
  async isStatusBadgeHealthy(): Promise<boolean> {
    const badge = await this.statusBadgeElement();
    return badge ? badge.hasClass('healthy') : false;
  }

  /** Gets the count of flavor capacity table rows. */
  async getFlavorRowCount(): Promise<number> {
    const rows = await this.tableRows();
    return rows.length;
  }

  /** Gets the text content of a flavor row at the given index. */
  async getFlavorRowText(index: number): Promise<string> {
    const rows = await this.tableRows();
    if (!rows[index]) {
      throw new Error(`No flavor row found at index ${index}`);
    }
    return rows[index].text();
  }

  /** Clicks the copy flag button. */
  async clickCopyFlagButton(): Promise<void> {
    const btn = await this.copyFlagButtonElement();
    if (!btn) {
      throw new Error('Copy flag button not found');
    }
    await btn.click();
  }

  /** Gets the copy flag button text. */
  async getCopyFlagButtonText(): Promise<string | null> {
    const btn = await this.copyFlagButtonElement();
    return btn ? btn.text() : null;
  }

  private readonly predictabilitySectionElement =
    this.locatorForOptional('.predictability-section');
  private readonly spinnerContainerElement = this.locatorForOptional(
    '.predictability-spinner-container',
  );

  /** Gets the text of the predictability section if present. */
  async getPredictabilitySectionText(): Promise<string | null> {
    const section = await this.predictabilitySectionElement();
    return section ? section.text() : null;
  }

  /** Gets the text of the predictability loading spinner container if present. */
  async getSpinnerContainerText(): Promise<string | null> {
    const spinner = await this.spinnerContainerElement();
    return spinner ? spinner.text() : null;
  }

  /** Clicks the queue card element. */
  async click(): Promise<void> {
    const card = await this.cardElement();
    await card.click();
  }
}
