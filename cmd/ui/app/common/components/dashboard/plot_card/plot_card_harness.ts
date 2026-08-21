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

import { SummaryBoxHarness } from '../summary_box/summary_box_harness';

/**
 * Component harness for PlotCardComponent.
 */
export class PlotCardHarness extends ComponentHarness {
  static hostSelector = 'xmc-plot-card';

  private readonly titleEl = this.locatorForOptional('.plot-title');
  private readonly canvasEl = this.locatorForOptional('.plot-canvas');
  private readonly summaryBox = this.locatorForOptional(SummaryBoxHarness);

  async isSummaryBox(): Promise<boolean> {
    return (await this.summaryBox()) !== null;
  }

  async getSummaryBoxHarness(): Promise<SummaryBoxHarness | null> {
    return this.summaryBox();
  }

  async getTitle(): Promise<string> {
    const el = await this.titleEl();
    return el ? el.text() : '';
  }

  async hasCanvas(): Promise<boolean> {
    return (await this.canvasEl()) !== null;
  }
}
