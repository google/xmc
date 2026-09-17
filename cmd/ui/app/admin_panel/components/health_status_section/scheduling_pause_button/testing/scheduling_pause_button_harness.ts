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
import { MatSlideToggleHarness } from '@angular/material/slide-toggle/testing';

/** Harness for interacting with <xmc-scheduling-pause-button> in tests. */
export class SchedulingPauseButtonHarness extends ComponentHarness {
  static hostSelector = 'xmc-scheduling-pause-button';

  private readonly _card = this.locatorFor('.scheduling-pause-card');
  private readonly _toggle = this.locatorForOptional(MatSlideToggleHarness);
  private readonly _title = this.locatorFor('.pause-title');
  private readonly _subtitle = this.locatorFor('.pause-subtitle');
  private readonly _icon = this.locatorFor('.pause-icon');

  async isPaused(): Promise<boolean> {
    const card = await this._card();
    return card.hasClass('is-paused');
  }

  async hasToggle(): Promise<boolean> {
    const toggle = await this._toggle();
    return toggle !== null;
  }

  async isChecked(): Promise<boolean> {
    const toggle = await this._toggle();
    return toggle ? toggle.isChecked() : false;
  }

  async isDisabled(): Promise<boolean> {
    const toggle = await this._toggle();
    return toggle ? toggle.isDisabled() : false;
  }

  async toggle(): Promise<void> {
    const toggle = await this._toggle();
    if (!toggle) {
      throw new Error('Pause slide toggle not found');
    }
    return toggle.toggle();
  }

  async getTitle(): Promise<string> {
    const title = await this._title();
    return title.text();
  }

  async getSubtitle(): Promise<string> {
    const subtitle = await this._subtitle();
    return subtitle.text();
  }

  async getIconName(): Promise<string> {
    const icon = await this._icon();
    return icon.text();
  }
}
