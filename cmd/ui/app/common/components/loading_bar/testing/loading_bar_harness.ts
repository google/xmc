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
import { MatProgressBarHarness } from '@angular/material/progress-bar/testing';

/** Harness for interacting with <xmc-loading-bar> in tests. */
export class LoadingBarHarness extends ComponentHarness {
  static hostSelector = 'xmc-loading-bar, [data-component-selector="xmc-loading-bar"]';

  // Use locatorFor to find the MatProgressBarHarness within this component's
  // template
  protected readonly progressBar = this.locatorFor(MatProgressBarHarness);

  /** Gets the value of the underlying progress bar. */
  async getValue(): Promise<number | null> {
    return (await this.progressBar()).getValue();
  }

  /** Returns true if the loading bar is in refreshing mode. */
  async isRefreshing(): Promise<boolean> {
    const progressBar = await this.progressBar();
    const host = await progressBar.host();
    return host.hasClass('refresh-loading');
  }

  /** Returns the mode of the progress bar. */
  async getMode(): Promise<string | null> {
    const progressBar = await this.progressBar();
    return progressBar.getMode();
  }
}
