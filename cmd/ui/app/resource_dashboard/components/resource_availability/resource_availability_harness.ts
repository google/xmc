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

import { ComponentHarness, TestElement } from '@angular/cdk/testing';

import { ErrorBannerHarness } from 'app/common/components/error_banner/error_banner_harness';

/** Test harness for a single per-accelerator summary card. */
export class AcceleratorCardHarness extends ComponentHarness {
  static hostSelector = '[data-testid="accelerator-card"]';

  private readonly titleEl = this.locatorFor('[data-testid="card-title"]');
  private readonly nodesEl = this.locatorFor('[data-testid="card-nodes"]');
  private readonly allocatedEl = this.locatorFor('[data-testid="kpi-allocated"] .kpi-value');
  private readonly unassignedEl = this.locatorFor('[data-testid="kpi-unassigned"] .kpi-value');
  private readonly readyEl = this.locatorFor('[data-testid="kpi-ready"] .kpi-value');
  private readonly reservedEl = this.locatorFor('[data-testid="kpi-reserved"] .kpi-value');

  async getTitle(): Promise<string> {
    return this.getTrimmedText(this.titleEl);
  }

  async getNodesText(): Promise<string> {
    return this.getTrimmedText(this.nodesEl);
  }

  async getAllocatedText(): Promise<string> {
    return this.getTrimmedText(this.allocatedEl);
  }

  async getUnassignedText(): Promise<string> {
    return this.getTrimmedText(this.unassignedEl);
  }

  async getReadyText(): Promise<string> {
    return this.getTrimmedText(this.readyEl);
  }

  async getReservedText(): Promise<string> {
    return this.getTrimmedText(this.reservedEl);
  }

  private async getTrimmedText(locator: () => Promise<TestElement>): Promise<string> {
    return (await (await locator()).text()).trim();
  }
}

/** Test harness for a single row in the physical inventory table. */
export class AvailabilityRowHarness extends ComponentHarness {
  static hostSelector = '[data-testid="availability-row"]';

  private readonly cells = this.locatorForAll('td');
  private readonly queuePills = this.locatorForAll('.pill-queue');

  /** Returns the trimmed text of all cells in the row. */
  async getCellTexts(): Promise<string[]> {
    const cellEls = await this.cells();
    return Promise.all(cellEls.map(async (cell) => (await cell.text()).trim()));
  }

  /** Returns the trimmed text of all ClusterQueue pills rendered in the row. */
  async getQueuePills(): Promise<string[]> {
    const pillEls = await this.queuePills();
    return Promise.all(pillEls.map(async (pill) => (await pill.text()).trim()));
  }
}

/** Test harness for ResourceAvailabilityComponent. */
export class ResourceAvailabilityHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-resource-availability';

  private readonly loadingEl = this.locatorForOptional('[data-testid="availability-loading"]');
  private readonly emptyEl = this.locatorForOptional('[data-testid="availability-empty"]');
  private readonly contentEl = this.locatorForOptional('[data-testid="availability-content"]');
  private readonly refreshBtn = this.locatorFor('[data-testid="availability-refresh-btn"]');
  private readonly errorBanner = this.locatorForOptional(ErrorBannerHarness);
  private readonly cards = this.locatorForAll(AcceleratorCardHarness);
  private readonly rows = this.locatorForAll(AvailabilityRowHarness);

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

  /** Gets all per-accelerator summary card harnesses. */
  async getAcceleratorCards(): Promise<AcceleratorCardHarness[]> {
    return this.cards();
  }

  /** Gets all physical inventory table row harnesses. */
  async getTableRows(): Promise<AvailabilityRowHarness[]> {
    return this.rows();
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
