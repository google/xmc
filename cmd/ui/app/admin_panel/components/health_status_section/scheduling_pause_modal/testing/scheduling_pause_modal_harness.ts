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

/** Harness for interacting with <xmc-scheduling-pause-modal> in tests. */
export class SchedulingPauseModalHarness extends ComponentHarness {
  static hostSelector = 'xmc-scheduling-pause-modal';

  private readonly _backdrop = this.locatorFor('.modal-backdrop');
  private readonly _title = this.locatorFor('.modal-title');
  private readonly _message = this.locatorFor('.modal-message');
  private readonly _confirmBtn = this.locatorFor('.modal-btn-confirm');
  private readonly _cancelBtn = this.locatorFor('.modal-btn-cancel');

  async getTitleText(): Promise<string> {
    const title = await this._title();
    return title.text();
  }

  async getMessageText(): Promise<string> {
    const msg = await this._message();
    return msg.text();
  }

  async getConfirmButtonText(): Promise<string> {
    const btn = await this._confirmBtn();
    return btn.text();
  }

  async isConfirmPauseButton(): Promise<boolean> {
    const btn = await this._confirmBtn();
    return btn.hasClass('btn-pause');
  }

  async isConfirmResumeButton(): Promise<boolean> {
    const btn = await this._confirmBtn();
    return btn.hasClass('btn-resume');
  }

  async clickConfirm(): Promise<void> {
    const btn = await this._confirmBtn();
    return btn.click();
  }

  async clickCancel(): Promise<void> {
    const btn = await this._cancelBtn();
    return btn.click();
  }

  async clickBackdrop(): Promise<void> {
    const backdrop = await this._backdrop();
    return backdrop.click();
  }
}
