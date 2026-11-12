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

/** Harness for interacting with <xmc-queue-user-allocation-bar> in tests. */
export class QueueUserAllocationBarHarness extends ComponentHarness {
  static hostSelector = 'xmc-queue-user-allocation-bar';

  private readonly hint = this.locatorForOptional('.allocation-hint');
  private readonly empty = this.locatorForOptional('.allocation-empty');
  private readonly title = this.locatorForOptional('.allocation-title');
  private readonly totals = this.locatorForOptional('.allocation-totals');
  private readonly bar = this.locatorForOptional('.allocation-bar');
  private readonly chipUsers = this.locatorForAll('.allocation-chip .chip-user');
  private readonly chipUnits = this.locatorForAll('.allocation-chip .chip-units');
  private readonly runningSegments = this.locatorForAll('.allocation-segment.running');

  private async optionalText(locator: () => Promise<{ text(): Promise<string> } | null>) {
    const el = await locator();
    return el ? (await el.text()).trim() : null;
  }

  /** Gets the empty-picker hint text, or null when hidden. */
  async getHintText(): Promise<string | null> {
    return this.optionalText(this.hint);
  }

  /** Gets the no-matching-jobs message, or null when hidden. */
  async getEmptyText(): Promise<string | null> {
    return this.optionalText(this.empty);
  }

  /** Gets the bar title, or null when hidden. */
  async getTitleText(): Promise<string | null> {
    return this.optionalText(this.title);
  }

  /** Gets the queue-wide running and pending totals label, or null when hidden. */
  async getTotalsText(): Promise<string | null> {
    return this.optionalText(this.totals);
  }

  /** Whether the stacked bar is rendered. */
  async hasBar(): Promise<boolean> {
    return (await this.bar()) !== null;
  }

  /** Gets each user chip's text as "<user> <units label>". */
  async getChipTexts(): Promise<string[]> {
    const [users, units] = await Promise.all([this.chipUsers(), this.chipUnits()]);
    return Promise.all(
      users.map(async (u, i) => `${(await u.text()).trim()} ${(await units[i].text()).trim()}`),
    );
  }

  /** Gets the inline width percentage of each user's running segment. */
  async getRunningSegmentWidths(): Promise<string[]> {
    const segments = await this.runningSegments();
    const styles = await Promise.all(segments.map((s) => s.getAttribute('style')));
    return styles.map((style) => /width:\s*([\d.]+%)/.exec(style ?? '')?.[1] ?? '');
  }
}
