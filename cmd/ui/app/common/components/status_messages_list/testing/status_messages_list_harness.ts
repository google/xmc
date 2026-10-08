// Copyright 2025 Google LLC
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

import { StatusMessagesListFilterHarness } from 'app/common/components/status_messages_list/status_messages_list_filter/testing/status_messages_list_filter_harness';

/** Harness for interacting with <xmc-status-messages-list> in tests. */
export class StatusMessagesListHarness extends ComponentHarness {
  static hostSelector =
    'xmc-status-messages-list, [data-component-selector="xmc-status-messages-list"]';

  private readonly messagesEls = this.locatorForAll('xmc-status-message');
  private readonly filterEl = this.locatorFor(StatusMessagesListFilterHarness);

  async getFilter(): Promise<StatusMessagesListFilterHarness> {
    return this.filterEl();
  }
  private readonly errorEl = this.locatorForOptional('.error-state p');
  private readonly loadMoreBtn = this.locatorForOptional('.load-more-container button');

  async getMessagesCount(): Promise<number> {
    const els = await this.messagesEls();
    return els.length;
  }

  async getMessageText(index: number): Promise<string> {
    const els = await this.messagesEls();
    if (index >= els.length) return '';
    return els[index].text();
  }

  async hasError(): Promise<boolean> {
    const el = await this.errorEl();
    return el !== null;
  }

  async getErrorText(): Promise<string> {
    const el = await this.errorEl();
    if (!el) return '';
    return el.text();
  }

  async clickLoadMore(): Promise<void> {
    const btn = await this.loadMoreBtn();
    if (!btn) {
      throw new Error('No load more button found.');
    }
    await btn.click();
  }

  async hasLoadMoreButton(): Promise<boolean> {
    const btn = await this.loadMoreBtn();
    return btn !== null;
  }
}
