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

/** Harness for interacting with <xmc-smart-resource-finder> in tests. */
export class SmartResourceFinderHarness extends ComponentHarness {
  static hostSelector = 'xmc-smart-resource-finder';

  private readonly amountInputs = this.locatorForAll('.query-amount-input');
  private readonly selectBoxes = this.locatorForAll('.query-select-box');
  private readonly removeButtons = this.locatorForAll('.remove-resource-btn');
  private readonly addButton = this.locatorFor('.add-resource-btn');
  private readonly optionElements = this.locatorForAll('.query-select-box option');

  /** Gets all option labels available in the resource type dropdowns. */
  async getResourceTypeOptions(): Promise<string[]> {
    const options = await this.optionElements();
    return Promise.all(options.map(async (opt) => (await opt.text()).trim()));
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
    const selects = await this.selectBoxes();
    return selects.length;
  }

  /** Gets the selected value of a resource type select at the given index. */
  async getResourceTypeSelectValue(index: number): Promise<string> {
    const selects = await this.selectBoxes();
    if (!selects[index]) {
      throw new Error(`No resource type select found at index ${index}`);
    }
    return selects[index].getProperty<string>('value');
  }

  /** Selects an option value for a resource type select at the given index. */
  async selectResourceType(index: number, optionValue: string): Promise<void> {
    const selects = await this.selectBoxes();
    if (!selects[index]) {
      throw new Error(`No resource type select found at index ${index}`);
    }
    const selectEl = selects[index];
    await selectEl.setInputValue(optionValue);
    await selectEl.dispatchEvent('change');
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

  private readonly dominantTexts = this.locatorForAll('.dominant-text');

  /** Gets whether a dominant label text is present. */
  async hasDominantBadge(): Promise<boolean> {
    const texts = await this.dominantTexts();
    return texts.length > 0;
  }

  /** Gets the text of the dominant label if present. */
  async getDominantBadgeText(): Promise<string | null> {
    const texts = await this.dominantTexts();
    return texts.length > 0 ? (await texts[0].text()).trim() : null;
  }

  /** Returns whether the select box at the given index has the dominant class. */
  async isDominantSelectBox(index: number): Promise<boolean> {
    const selects = await this.selectBoxes();
    if (!selects[index]) {
      throw new Error(`No resource type select found at index ${index}`);
    }
    return selects[index].hasClass('dominant-select-box');
  }

  /** Clicks the + Add resource type button. */
  async clickAddButton(): Promise<void> {
    const addBtn = await this.addButton();
    await addBtn.click();
  }
}
