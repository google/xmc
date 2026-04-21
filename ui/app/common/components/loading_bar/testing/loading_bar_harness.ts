import {ComponentHarness} from '@angular/cdk/testing';
import {MatProgressBarHarness} from '@angular/material/progress-bar/testing';

/** Harness for interacting with <xmc-loading-bar> in tests. */
export class LoadingBarHarness extends ComponentHarness {
  static hostSelector = 'xmc-loading-bar';

  // Use locatorFor to find the MatProgressBarHarness within this component's
  // template
  protected readonly progressBar = this.locatorFor(MatProgressBarHarness);

  /** Gets the value of the underlying progress bar. */
  async getValue(): Promise<number|null> {
    return (await this.progressBar()).getValue();
  }

  /** Returns true if the loading bar is in refreshing mode. */
  async isRefreshing(): Promise<boolean> {
    const progressBar = await this.progressBar();
    const host = await progressBar.host();
    return host.hasClass('refresh-loading');
  }

  /** Returns the mode of the progress bar. */
  async getMode(): Promise<string|null> {
    const progressBar = await this.progressBar();
    return progressBar.getMode();
  }
}
