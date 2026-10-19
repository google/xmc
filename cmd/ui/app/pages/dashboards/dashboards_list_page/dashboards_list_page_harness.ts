// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { ComponentHarness } from '@angular/cdk/testing';
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatTableHarness } from '@angular/material/table/testing';

/**
 * Component harness for DashboardsListPage.
 */
export class DashboardsListPageHarness extends ComponentHarness {
  static hostSelector = 'xmc-dashboards-list-page';

  private readonly refreshButton = this.locatorFor(
    MatButtonHarness.with({ selector: '.action-button' }),
  );
  private readonly prevPageButton = this.locatorFor(
    MatButtonHarness.with({ selector: '[data-test-id="previous-page-button"]' }),
  );
  private readonly nextPageButton = this.locatorFor(
    MatButtonHarness.with({ selector: '[data-test-id="next-page-button"]' }),
  );
  private readonly pageLabelEl = this.locatorForOptional('.page-label');
  private readonly table = this.locatorFor(MatTableHarness);

  async clickRefresh(): Promise<void> {
    return (await this.refreshButton()).click();
  }

  async clickNextPage(): Promise<void> {
    return (await this.nextPageButton()).click();
  }

  async clickPreviousPage(): Promise<void> {
    return (await this.prevPageButton()).click();
  }

  async isNextPageDisabled(): Promise<boolean> {
    return (await this.nextPageButton()).isDisabled();
  }

  async isPreviousPageDisabled(): Promise<boolean> {
    return (await this.prevPageButton()).isDisabled();
  }

  async isRefreshDisabled(): Promise<boolean> {
    return (await this.refreshButton()).isDisabled();
  }

  async getPageLabelText(): Promise<string | null> {
    const el = await this.pageLabelEl();
    return el ? el.text() : null;
  }

  async getTable(): Promise<MatTableHarness> {
    return this.table();
  }
}
