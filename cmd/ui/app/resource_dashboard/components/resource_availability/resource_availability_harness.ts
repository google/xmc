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

import { ErrorBannerHarness } from 'app/common/components/error_banner/error_banner_harness';

/** Test harness for ResourceAvailabilityComponent. */
export class ResourceAvailabilityHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-resource-availability';

  private readonly loadingEl = this.locatorForOptional('[data-testid="availability-loading"]');
  private readonly emptyEl = this.locatorForOptional('[data-testid="availability-empty"]');
  private readonly contentEl = this.locatorForOptional('[data-testid="availability-content"]');
  private readonly refreshBtn = this.locatorFor('[data-testid="availability-refresh-btn"]');
  private readonly errorBanner = this.locatorForOptional(ErrorBannerHarness);

  /** Checks whether the loading state is currently shown. */
  async isLoading(): Promise<boolean> {
    return (await this.loadingEl()) !== null;
  }

  /** Gets the error banner harness if an error is displayed. */
  async getErrorBanner(): Promise<ErrorBannerHarness | null> {
    return this.errorBanner();
  }

  /** Checks whether the empty state is currently shown. */
  async isEmpty(): Promise<boolean> {
    return (await this.emptyEl()) !== null;
  }

  /** Checks whether the loaded content container is currently shown. */
  async hasContent(): Promise<boolean> {
    return (await this.contentEl()) !== null;
  }

  /** Gets the text content of the loaded content container, or null if absent. */
  async getContentText(): Promise<string | null> {
    const el = await this.contentEl();
    return el ? (await el.text()).trim() : null;
  }

  /** Clicks the refresh button. */
  async clickRefresh(): Promise<void> {
    const btn = await this.refreshBtn();
    await btn.click();
  }

  /** Checks whether the refresh button is disabled. */
  async isRefreshDisabled(): Promise<boolean> {
    const btn = await this.refreshBtn();
    return (await btn.getProperty<boolean>('disabled')) ?? false;
  }
}
