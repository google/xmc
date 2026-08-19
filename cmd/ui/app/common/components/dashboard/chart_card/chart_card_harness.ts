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

import { PlotCardHarness } from '../plot_card/plot_card_harness';
import { SummaryBoxHarness } from '../summary_box/summary_box_harness';

/**
 * Component harness for ChartCardComponent.
 */
export class ChartCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-chart-card';

  private readonly headerLink = this.locatorFor('.chart-header-link');
  private readonly summaryBoxes = this.locatorForAll(SummaryBoxHarness);
  private readonly plotCards = this.locatorForAll(PlotCardHarness);

  async getHeaderTitle(): Promise<string> {
    return (await this.headerLink()).text();
  }

  async getSummaryBoxCount(): Promise<number> {
    return (await this.summaryBoxes()).length;
  }

  async getPlotCardCount(): Promise<number> {
    return (await this.plotCards()).length;
  }
}
