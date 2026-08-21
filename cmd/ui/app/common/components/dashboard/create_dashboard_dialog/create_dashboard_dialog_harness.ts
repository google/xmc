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

/** Harness for interacting with individual plot item cards in tests. */
export class PlotItemHarness extends ComponentHarness {
  static hostSelector = '.plot-item-card';

  private readonly chip = this.locatorFor('.plot-type-chip');
  private readonly title = this.locatorFor('.plot-item-title');
  private readonly metric = this.locatorFor('.plot-item-metric span');
  private readonly removeBtn = this.locatorFor('.remove-plot-button');

  async getChipText(): Promise<string> {
    return (await this.chip()).text();
  }

  async isKpiChip(): Promise<boolean> {
    return (await this.chip()).hasClass('kpi-chip');
  }

  async isCurveChip(): Promise<boolean> {
    return (await this.chip()).hasClass('curve-chip');
  }

  async getTitle(): Promise<string> {
    return (await this.title()).text();
  }

  async getMetricText(): Promise<string> {
    return (await this.metric()).text();
  }

  async remove(): Promise<void> {
    const btn = await this.removeBtn();
    await btn.click();
  }
}

/** Harness for interacting with comparison experiment chips in tests. */
export class ComparisonItemHarness extends ComponentHarness {
  static hostSelector = '.comparison-item-chip';

  private readonly text = this.locatorFor('.comp-text');
  private readonly removeBtn = this.locatorFor('.remove-comp-button');

  async getText(): Promise<string> {
    return (await this.text()).text();
  }

  async remove(): Promise<void> {
    const btn = await this.removeBtn();
    await btn.click();
  }
}

/** Component harness for CreateDashboardDialogComponent. */
export class CreateDashboardDialogHarness extends ComponentHarness {
  static hostSelector = 'xmc-create-dashboard-dialog';

  private readonly plotItems = this.locatorForAll(PlotItemHarness);
  private readonly resetPlotsBtn = this.locatorFor('.reset-plots-button');
  private readonly addPlotBtn = this.locatorFor('.add-plot-btn');
  private readonly comparisonItems = this.locatorForAll(ComparisonItemHarness);
  private readonly addComparisonBtn = this.locatorFor('.add-comp-btn');
  private readonly submitBtn = this.locatorFor('.submit-button');
  private readonly cancelBtn = this.locatorFor('.dialog-actions button:not(.submit-button)');

  async getPlotItems(): Promise<PlotItemHarness[]> {
    return this.plotItems();
  }

  async getPlotCount(): Promise<number> {
    return (await this.plotItems()).length;
  }

  async removePlot(index: number): Promise<void> {
    const items = await this.getPlotItems();
    if (!items[index]) {
      throw new Error(`No plot item found at index ${index}`);
    }
    await items[index].remove();
  }

  async clickResetPlotsToDefault(): Promise<void> {
    const btn = await this.resetPlotsBtn();
    await btn.click();
  }

  async clickAddPlot(): Promise<void> {
    const btn = await this.addPlotBtn();
    await btn.click();
  }

  async isAddPlotButtonDisabled(): Promise<boolean> {
    const btn = await this.addPlotBtn();
    return btn.getProperty<boolean>('disabled');
  }

  async getComparisonItems(): Promise<ComparisonItemHarness[]> {
    return this.comparisonItems();
  }

  async getComparisonCount(): Promise<number> {
    return (await this.comparisonItems()).length;
  }

  async removeComparison(index: number): Promise<void> {
    const items = await this.getComparisonItems();
    if (!items[index]) {
      throw new Error(`No comparison item found at index ${index}`);
    }
    await items[index].remove();
  }

  async getAddComparisonButtonText(): Promise<string> {
    return (await this.addComparisonBtn()).text();
  }

  async isAddComparisonButtonDisabled(): Promise<boolean> {
    const btn = await this.addComparisonBtn();
    return btn.getProperty<boolean>('disabled');
  }

  async clickAddComparison(): Promise<void> {
    const btn = await this.addComparisonBtn();
    await btn.click();
  }

  async clickSubmit(): Promise<void> {
    const btn = await this.submitBtn();
    await btn.click();
  }

  async isSubmitButtonDisabled(): Promise<boolean> {
    const btn = await this.submitBtn();
    return btn.getProperty<boolean>('disabled');
  }

  async clickCancel(): Promise<void> {
    const btn = await this.cancelBtn();
    await btn.click();
  }
}
