import {ComponentHarness} from '@angular/cdk/testing';
import {StatusMessagesListFilterHarness} from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list_filter/testing/status_messages_list_filter_harness';

/** Harness for interacting with <xmc-status-messages-list> in tests. */
export class StatusMessagesListHarness extends ComponentHarness {
  static hostSelector = 'xmc-status-messages-list';

  private readonly messagesEls = this.locatorForAll('xmc-status-message');
  private readonly filterEl = this.locatorFor(StatusMessagesListFilterHarness);

  async getFilter(): Promise<StatusMessagesListFilterHarness> {
    return this.filterEl();
  }
  private readonly errorEl = this.locatorForOptional('.error-state p');
  private readonly loadMoreBtn =
      this.locatorForOptional('.load-more-container button');

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
