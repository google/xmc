import {ComponentHarness} from '@angular/cdk/testing';
import {MatRowHarness, MatTableHarness, TableHarnessFilters} from '@angular/material/table/testing';
import {LoadingBarHarness} from 'xmanager_cloud/ui/app/common/components/loading_bar/testing/loading_bar_harness';

/** Harness for interacting with <xmc-work-units-table> in tests. */
export class WorkUnitsTableHarness extends ComponentHarness {
  static hostSelector = 'xmc-work-units-table';

  private readonly _loadingBar = this.locatorForOptional(LoadingBarHarness);
  private readonly _table = this.locatorForOptional(MatTableHarness);
  private readonly _noWorkUnitsMessage =
      this.locatorForOptional('.no-work-units-message');
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

  async getTable(): Promise<MatTableHarness|null> {
    return this._table();
  }

  async getRows(filter: TableHarnessFilters = {}): Promise<MatRowHarness[]> {
    const table = await this._table();
    if (!table) {
      return [];
    }
    return table.getRows(filter);
  }

  async hasNoWorkUnitsMessage(): Promise<boolean> {
    return !!(await this._noWorkUnitsMessage());
  }

  async hasErrorMessage(): Promise<boolean> {
    return !!(await this._errorMessage());
  }
}
