// Copyright 2026 Google LLC
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
import { MatSelectHarness } from '@angular/material/select/testing';
import { MatTooltipHarness } from '@angular/material/tooltip/testing';

/** Harness for interacting with <xmc-smart-resource-finder> in tests. */
export class SmartResourceFinderHarness extends ComponentHarness {
  static hostSelector = 'xmc-smart-resource-finder';

  private readonly amountInputs = this.locatorForAll('.query-amount-input');
  private readonly selectHarnesses = this.locatorForAll(MatSelectHarness);
  private readonly removeButtons = this.locatorForAll('.remove-resource-btn');
  private readonly addButton = this.locatorFor('.add-resource-btn');
  private readonly amountLegends = this.locatorForAll('.amount-box .box-legend');
  private readonly resourceLegends = this.locatorForAll('.resource-select-box .box-legend');
  private readonly primaryHelpIcons = this.locatorForAll('.primary-resource-help-icon');

  /** Gets all option labels available in the resource type dropdowns. */
  async getResourceTypeOptions(): Promise<string[]> {
    const selects = await this.selectHarnesses();
    if (selects.length === 0) return [];
    await selects[0].open();
    const options = await selects[0].getOptions();
    const texts = await Promise.all(options.map((opt) => opt.getText()));
    await selects[0].close();
    return texts;
  }

  /** Gets the amount input value as a string for the given index. */
  async getAmountValue(index = 0): Promise<string> {
    const inputs = await this.amountInputs();
    if (!inputs[index]) {
      throw new Error(`No amount input found at index ${index}`);
    }
    return inputs[index].getProperty<string>('value');
  }

  /** Sets the amount input value for the given index and dispatches input change events. */
  async setAmountValue(val: string, index = 0): Promise<void> {
    const inputs = await this.amountInputs();
    if (!inputs[index]) {
      throw new Error(`No amount input found at index ${index}`);
    }
    const input = inputs[index];
    await input.clear();
    if (val !== '') {
      await input.sendKeys(val);
    }
    await input.dispatchEvent('input');
    await input.dispatchEvent('change');
  }

  /** Gets the count of resource type select dropdowns. */
  async getResourceTypeSelectCount(): Promise<number> {
    const selects = await this.selectHarnesses();
    return selects.length;
  }

  /** Gets the selected value of a resource type select at the given index. */
  async getResourceTypeSelectValue(index: number): Promise<string> {
    const selects = await this.selectHarnesses();
    if (!selects[index]) {
      throw new Error(`No resource type select found at index ${index}`);
    }
    const text = await selects[index].getValueText();
    return text === '-' ? '' : text;
  }

  /** Selects an option value for a resource type select at the given index. */
  async selectResourceType(index: number, optionValue: string): Promise<void> {
    const selects = await this.selectHarnesses();
    if (!selects[index]) {
      throw new Error(`No resource type select found at index ${index}`);
    }
    await selects[index].open();
    const optText = optionValue === '' ? '-' : optionValue;
    await selects[index].clickOptions({ text: optText });
  }

  /** Gets the count of remove resource filter buttons. */
  async getRemoveButtonCount(): Promise<number> {
    const removeBtns = await this.removeButtons();
    return removeBtns.length;
  }

  /** Clicks the remove resource filter button at the given index. */
  async clickRemoveButton(index: number): Promise<void> {
    const removeBtns = await this.removeButtons();
    if (!removeBtns[index]) {
      throw new Error(`No remove button found at index ${index}`);
    }
    await removeBtns[index].click();
  }

  /** Gets the Amount box legend label. */
  async getAmountBoxLabel(): Promise<string> {
    const legends = await this.amountLegends();
    if (legends.length === 0) {
      throw new Error('No amount legend found');
    }
    return (await legends[0].text()).trim();
  }

  /** Gets the resource type box legend label at the given index. */
  async getResourceBoxLabel(index = 0): Promise<string> {
    const legends = await this.resourceLegends();
    if (!legends[index]) {
      throw new Error(`No resource legend found at index ${index}`);
    }
    return (await legends[index].text()).trim();
  }

  /** Gets whether the primary resource help icon is present. */
  async hasPrimaryResourceHelpIcon(): Promise<boolean> {
    const icons = await this.primaryHelpIcons();
    return icons.length > 0;
  }

  /** Gets the primary resource help icon tooltip harness. */
  async getPrimaryResourceHelpTooltip(): Promise<MatTooltipHarness | null> {
    return this.locatorForOptional(
      MatTooltipHarness.with({ selector: '.primary-resource-help-icon' }),
    )();
  }

  /** Clicks the + Add resource type button. */
  async clickAddButton(): Promise<void> {
    const addBtn = await this.addButton();
    await addBtn.click();
  }
}
