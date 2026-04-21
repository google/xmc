import {ComponentHarness} from '@angular/cdk/testing';
import {MatTableHarness} from '@angular/material/table/testing';
import {LoadingBarHarness} from 'xmanager_cloud/ui/app/common/components/loading_bar/testing/loading_bar_harness';

/** Harness for interacting with <xmc-experiments-table> in tests. */
export class ExperimentsTableHarness extends ComponentHarness {
  static hostSelector = 'xmc-experiments-table';

  private readonly _loadingBar = this.locatorForOptional(LoadingBarHarness);
  private readonly _table = this.locatorForOptional(MatTableHarness);
  private readonly _noExperimentsMessage =
      this.locatorForOptional('.no-experiments-message');
  private readonly _tableContainer =
      this.locatorForOptional('.table-container');

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

  async isTablePresent(): Promise<boolean> {
    return !!(await this._tableContainer());
  }

  async hasNoExperimentsMessage(): Promise<boolean> {
    return !!(await this._noExperimentsMessage());
  }
}
