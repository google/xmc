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
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatSortHarness } from '@angular/material/sort/testing';
import {
  MatCellHarness,
  MatRowHarness,
  MatTableHarness,
  TableHarnessFilters,
} from '@angular/material/table/testing';
import { MatTooltipHarness } from '@angular/material/tooltip/testing';

import { LoadingBarHarness } from 'app/common/components/loading_bar/testing/loading_bar_harness';

/** Harness for interacting with <xmc-work-units-table> in tests. */
export class WorkUnitsTableHarness extends ComponentHarness {
  static hostSelector = 'xmc-work-units-table, [data-component-selector="xmc-work-units-table"]';

  private readonly _loadingBar = this.locatorForOptional(LoadingBarHarness);
  private readonly _table = this.locatorForOptional(MatTableHarness);
  private readonly _sort = this.locatorFor(MatSortHarness);
  private readonly _pageLabel = this.locatorFor('.paginator .page-label');
  private readonly _noWorkUnitsMessage = this.locatorForOptional('.no-work-units-message');
  private readonly _errorMessage = this.locatorForOptional('.error-message');

  async hasLoadingBar(): Promise<boolean> {
    return !!(await this._loadingBar());
  }

  /**
   * Returns true if the component is in initial loading state
   * (loading bar without refreshing).
   **/
  async isLoading(): Promise<boolean> {
    const loadingBar = await this._loadingBar();
    if (!loadingBar) return false;
    return !(await loadingBar.isRefreshing());
  }

  /**
   * Returns true if the component is in refreshing state
   * (loading bar with refreshing).
   **/
  async isRefreshing(): Promise<boolean> {
    const loadingBar = await this._loadingBar();
    if (!loadingBar) return false;
    return loadingBar.isRefreshing();
  }

  async hasTable(): Promise<boolean> {
    return !!(await this._table());
  }

  async getTable(): Promise<MatTableHarness | null> {
    return this._table();
  }

  async getRows(filter: TableHarnessFilters = {}): Promise<MatRowHarness[]> {
    const table = await this._table();
    if (!table) {
      return [];
    }
    return table.getRows(filter);
  }

  /**
   * Returns the non-detail data rows.
   */
  async getDataRows(): Promise<MatRowHarness[]> {
    return this.getRows({ selector: ':not(.detail-row)' });
  }

  async hasNoWorkUnitsMessage(): Promise<boolean> {
    return !!(await this._noWorkUnitsMessage());
  }

  /** Returns the sort harness of the work units table. */
  async getSort(): Promise<MatSortHarness> {
    return this._sort();
  }

  /** Returns the trimmed paginator page label, e.g. "Page 1". */
  async getPageLabel(): Promise<string> {
    return (await (await this._pageLabel()).text()).trim();
  }

  async hasErrorMessage(): Promise<boolean> {
    return !!(await this._errorMessage());
  }

  /**
   * Returns the URLs action cell for a given data row index, or null if absent.
   */
  async getUrlsCell(rowIndex: number): Promise<MatCellHarness | null> {
    const rows = await this.getDataRows();
    const row = rows[rowIndex];
    if (!row) {
      return null;
    }
    const cells = await row.getCells({ columnName: 'Urls' });
    return cells[0] ?? null;
  }

  /**
   * Returns the Run Profile button harness for a given row index, or null if absent.
   */
  async getRunProfileButton(rowIndex: number): Promise<MatButtonHarness | null> {
    const cell = await this.getUrlsCell(rowIndex);
    return cell ? cell.getHarnessOrNull(MatButtonHarness.with({ text: 'troubleshoot' })) : null;
  }

  /**
   * Returns the Run Profile tooltip harness for a given row index, or null if absent.
   */
  async getRunProfileTooltip(rowIndex: number): Promise<MatTooltipHarness | null> {
    const button = await this.getRunProfileButton(rowIndex);
    if (!button) {
      return null;
    }
    const cell = await this.getUrlsCell(rowIndex);
    return cell ? cell.getHarnessOrNull(MatTooltipHarness) : null;
  }

  /**
   * Returns link attributes (href, target, rel) for the Run Profile button, or null if absent.
   */
  async getRunProfileLink(
    rowIndex: number,
  ): Promise<{ href: string | null; target: string | null; rel: string | null } | null> {
    const button = await this.getRunProfileButton(rowIndex);
    if (!button) {
      return null;
    }
    const host = await button.host();
    return {
      href: await host.getAttribute('href'),
      target: await host.getAttribute('target'),
      rel: await host.getAttribute('rel'),
    };
  }
}
