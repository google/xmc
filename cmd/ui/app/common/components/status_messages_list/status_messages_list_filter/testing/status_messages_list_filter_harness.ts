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
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatDatepickerInputHarness } from '@angular/material/datepicker/testing';
import { MatSelectHarness } from '@angular/material/select/testing';

/** Harness for interacting with <xmc-status-messages-list-filter> in tests. */
export class StatusMessagesListFilterHarness extends ComponentHarness {
  static hostSelector =
    'xmc-status-messages-list-filter, [data-component-selector="xmc-status-messages-list-filter"]';

  private readonly severitySelect = this.locatorFor(
    MatSelectHarness.with({ selector: '[data-testid="severity-select"]' }),
  );
  private readonly sourceSelect = this.locatorFor(
    MatSelectHarness.with({ selector: '[data-testid="source-select"]' }),
  );
  private readonly sortOrderSelect = this.locatorFor(
    MatSelectHarness.with({ selector: '[data-testid="sort-order-select"]' }),
  );
  private readonly timePresetSelect = this.locatorFor(
    MatSelectHarness.with({ selector: '[data-testid="time-preset-select"]' }),
  );
  private readonly refreshButton = this.locatorFor(
    MatButtonHarness.with({ selector: '.refresh-button' }),
  );
  private readonly startPicker = this.locatorFor(
    MatDatepickerInputHarness.with({ selector: '#custom-date-start' }),
  );
  private readonly endPicker = this.locatorFor(
    MatDatepickerInputHarness.with({ selector: '#custom-date-end' }),
  );

  async clickRefresh(): Promise<void> {
    const button = await this.refreshButton();
    await button.click();
  }

  async selectSeverity(option: string): Promise<void> {
    const select = await this.severitySelect();
    await select.open();
    await select.clickOptions({ text: option });
  }

  async selectSource(option: string): Promise<void> {
    const select = await this.sourceSelect();
    await select.open();
    await select.clickOptions({ text: option });
  }

  async selectOnlySeverity(option: string): Promise<void> {
    await this.selectOnly(await this.severitySelect(), option);
  }

  async selectOnlySource(option: string): Promise<void> {
    await this.selectOnly(await this.sourceSelect(), option);
  }

  async selectSortOrder(option: string): Promise<void> {
    const select = await this.sortOrderSelect();
    await select.open();
    await select.clickOptions({ text: option });
  }

  async selectTimePreset(option: string): Promise<void> {
    const select = await this.timePresetSelect();
    await select.open();
    await select.clickOptions({ text: option });
  }

  async setCustomStart(value: string): Promise<void> {
    const picker = await this.startPicker();
    await picker.setValue(value);
  }

  async setCustomEnd(value: string): Promise<void> {
    const picker = await this.endPicker();
    await picker.setValue(value);
  }

  async getSeverityValue(): Promise<string> {
    const select = await this.severitySelect();
    return select.getValueText();
  }

  async getSourceValue(): Promise<string> {
    const select = await this.sourceSelect();
    return select.getValueText();
  }

  async getSortOrderValue(): Promise<string> {
    const select = await this.sortOrderSelect();
    return select.getValueText();
  }

  private async selectOnly(select: MatSelectHarness, option: string): Promise<void> {
    await select.open();
    const options = await select.getOptions();
    const states = await Promise.all(
      options.map(async (o) => ({
        text: await o.getText(),
        isSelected: await o.isSelected(),
        option: o,
      })),
    );

    for (const s of states) {
      if (s.text === option) {
        if (!s.isSelected) {
          await s.option.click();
        }
      } else {
        if (s.isSelected) {
          await s.option.click();
        }
      }
    }
  }
}
