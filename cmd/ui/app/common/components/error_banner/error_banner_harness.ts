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

/** Test harness for ErrorBannerComponent. */
export class ErrorBannerHarness extends ComponentHarness {
  static hostSelector = 'xmc-error-banner';

  private readonly _banner = this.locatorForOptional('.error-banner');
  private readonly _text = this.locatorForOptional('.error-text');
  private readonly _retryButton = this.locatorForOptional('.retry-button');

  /** Checks whether the error banner is rendered. */
  async isVisible(): Promise<boolean> {
    const banner = await this._banner();
    return banner !== null;
  }

  /** Gets the message text displayed in the error banner. */
  async getMessage(): Promise<string | null> {
    const text = await this._text();
    return text ? text.text() : null;
  }

  /** Checks whether the retry button is rendered. */
  async hasRetryButton(): Promise<boolean> {
    const btn = await this._retryButton();
    return btn !== null;
  }

  /** Gets the text of the retry button. */
  async getRetryButtonText(): Promise<string | null> {
    const btn = await this._retryButton();
    return btn ? btn.text() : null;
  }

  /** Clicks the retry button. */
  async clickRetry(): Promise<void> {
    const btn = await this._retryButton();
    if (!btn) {
      throw new Error('Retry button not found on ErrorBanner');
    }
    await btn.click();
  }
}
