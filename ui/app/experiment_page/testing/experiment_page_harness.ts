import {ComponentHarness} from '@angular/cdk/testing';
import {MatButtonHarness} from '@angular/material/button/testing';
import {MatTabGroupHarness} from '@angular/material/tabs/testing';
import {LoadingBarHarness} from 'xmanager_cloud/ui/app/common/components/loading_bar/testing/loading_bar_harness';

/** Harness for interacting with <xmc-experiment-page> in tests. */
export class ExperimentPageHarness extends ComponentHarness {
  static hostSelector = 'xmc-experiment-page';

  private readonly _loadingBar = this.locatorForOptional(LoadingBarHarness);
  private readonly _title = this.locatorForOptional('.experiment-title');
  private readonly _stopButton = this.locatorForOptional(
      MatButtonHarness.with({text: /Stop Experiment/}),
  );
  private readonly _restartButton = this.locatorFor(
      MatButtonHarness.with({text: /Restart Experiment/}),
  );
  private readonly _editButton = this.locatorFor(
      MatButtonHarness.with({text: /Edit Experiment/}),
  );
  private readonly _tabGroup = this.locatorFor(MatTabGroupHarness);

  async hasLoadingBar(): Promise<boolean> {
    return !!(await this._loadingBar());
  }

  async isLoading(): Promise<boolean> {
    const loadingBar = await this._loadingBar();
    if (!loadingBar) return false;
    return !(await loadingBar.isRefreshing());
  }

  async isRefreshing(): Promise<boolean> {
    const loadingBar = await this._loadingBar();
    if (!loadingBar) return false;
    return loadingBar.isRefreshing();
  }

  async hasTitle(): Promise<boolean> {
    return !!(await this._title());
  }

  async clickStopExperiment(): Promise<void> {
    const button = await this._stopButton();
    if (!button) {
      throw new Error('Stop Experiment button not found.');
    }
    await button.click();
  }

  async clickRestartExperiment(): Promise<void> {
    const button = await this._restartButton();
    await button.click();
  }

  async clickEditExperiment(): Promise<void> {
    const button = await this._editButton();
    await button.click();
  }

  async hasStatusHistoryTab(): Promise<boolean> {
    const tabGroup = await this._tabGroup();
    const tabs = await tabGroup.getTabs({label: 'Status History'});
    return tabs.length > 0;
  }
}
