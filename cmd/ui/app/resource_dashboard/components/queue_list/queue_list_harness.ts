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

import { QueueCardHarness } from 'app/resource_dashboard/components/queue_card/queue_card_harness';

/** Harness for interacting with <xmc-rod-queue-list> in tests. */
export class QueueListHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-queue-list';

  private readonly toggleButtons = this.locatorForAll('.toggle-pill');
  private readonly queueCards = this.locatorForAll(QueueCardHarness);
  private readonly loadingState = this.locatorForOptional('.loading-state');
  private readonly emptyState = this.locatorForOptional('.empty-state');

  /** Gets all velocity timeframe toggle button labels. */
  async getVelocityToggleLabels(): Promise<string[]> {
    const buttons = await this.toggleButtons();
    return Promise.all(buttons.map(async (b) => (await b.text()).trim()));
  }

  /** Gets the active velocity timeframe toggle button label. */
  async getActiveVelocityToggleLabel(): Promise<string | null> {
    const buttons = await this.toggleButtons();
    for (const btn of buttons) {
      if (await btn.hasClass('active-toggle')) {
        return (await btn.text()).trim();
      }
    }
    return null;
  }

  /** Clicks a velocity timeframe toggle button by index. */
  async selectVelocityPeriodIndex(index: number): Promise<void> {
    const buttons = await this.toggleButtons();
    if (!buttons[index]) {
      throw new Error(`No velocity toggle button found at index ${index}`);
    }
    await buttons[index].click();
  }

  /** Clicks a velocity timeframe toggle button by its label text. */
  async selectVelocityPeriod(periodLabel: string): Promise<void> {
    const buttons = await this.toggleButtons();
    for (const btn of buttons) {
      const text = (await btn.text()).trim();
      if (text === periodLabel) {
        await btn.click();
        return;
      }
    }
    throw new Error(`Velocity toggle button with label "${periodLabel}" not found`);
  }

  /** Gets all queue card harnesses. */
  async getQueueCards(): Promise<QueueCardHarness[]> {
    return this.queueCards();
  }

  /** Gets a queue card harness by index. */
  async getQueueCard(index: number): Promise<QueueCardHarness> {
    const cards = await this.queueCards();
    if (!cards[index]) {
      throw new Error(`No queue card found at index ${index}`);
    }
    return cards[index];
  }

  /** Gets the count of queue cards. */
  async getQueueCardCount(): Promise<number> {
    const cards = await this.queueCards();
    return cards.length;
  }

  /** Returns true if the loading state message is displayed. */
  async isLoading(): Promise<boolean> {
    const el = await this.loadingState();
    return el !== null;
  }

  /** Returns true if the empty state message is displayed. */
  async isEmpty(): Promise<boolean> {
    const el = await this.emptyState();
    return el !== null;
  }
}
