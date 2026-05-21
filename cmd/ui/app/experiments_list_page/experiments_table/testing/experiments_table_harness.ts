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
