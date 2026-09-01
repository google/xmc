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

import { PlotCardHarness } from 'xmanager_cloud/ui/app/common/components/dashboard/plot_card/plot_card_harness';
import { SummaryBoxHarness } from 'xmanager_cloud/ui/app/common/components/dashboard/summary_box/summary_box_harness';

/**
 * Component harness for ChartArtifactView.
 */
export class ChartArtifactViewHarness extends ComponentHarness {
  static hostSelector = 'xmc-chart-artifact-view';

  private readonly title = this.locatorFor('.chart-artifact-title');
  private readonly notice = this.locatorForOptional('.notice-container');
  private readonly metricChips = this.locatorForAll('.metric-chip');
  private readonly summaryBoxes = this.locatorForAll(SummaryBoxHarness);
  private readonly plotCards = this.locatorForAll(PlotCardHarness);

  async getTitle(): Promise<string> {
    return (await this.title()).text();
  }

  /** The explanation shown in place of the plots, or null when the chart is renderable. */
  async getNotice(): Promise<string | null> {
    const notice = await this.notice();
    return notice ? notice.text() : null;
  }

  async getMetricNames(): Promise<string[]> {
    return Promise.all((await this.metricChips()).map((chip) => chip.text()));
  }

  async getSummaryBoxCount(): Promise<number> {
    return (await this.summaryBoxes()).length;
  }

  async getPlotCardCount(): Promise<number> {
    return (await this.plotCards()).length;
  }
}
