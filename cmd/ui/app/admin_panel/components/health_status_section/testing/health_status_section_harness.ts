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

import { ErrorBannerHarness } from 'app/common/components/error_banner/error_banner_harness';

import { HealthStatusGroupCardHarness } from '../health_status_group_card/testing/health_status_group_card_harness';
import { SchedulingPauseButtonHarness } from '../scheduling_pause_button/testing/scheduling_pause_button_harness';
import { SchedulingPauseModalHarness } from '../scheduling_pause_modal/testing/scheduling_pause_modal_harness';

/** Harness for interacting with <xmc-health-status-section> in tests. */
export class HealthStatusSectionHarness extends ComponentHarness {
  static hostSelector = 'xmc-health-status-section';

  private readonly _title = this.locatorFor('.card-title');
  private readonly _refreshBtn = this.locatorFor('.refresh-button');
  private readonly _overallBadge = this.locatorFor('.status-badge');
  private readonly _errorBanner = this.locatorForOptional(ErrorBannerHarness);
  private readonly _pauseButton = this.locatorForOptional(SchedulingPauseButtonHarness);
  private readonly _modal = this.locatorForOptional(SchedulingPauseModalHarness);

  async getErrorBannerText(): Promise<string | null> {
    const banner = await this._errorBanner();
    return banner ? banner.getMessage() : null;
  }

  async getTitleText(): Promise<string> {
    const el = await this._title();
    return el.text();
  }

  async clickRefresh(): Promise<void> {
    const btn = await this._refreshBtn();
    return btn.click();
  }

  async isRefreshDisabled(): Promise<boolean> {
    const btn = await this._refreshBtn();
    return btn.getProperty<boolean>('disabled');
  }

  async getRefreshButtonText(): Promise<string> {
    const btn = await this._refreshBtn();
    return btn.text();
  }

  async getOverallStatusText(): Promise<string> {
    const badge = await this._overallBadge();
    return badge.text();
  }

  async getGroupCards(): Promise<HealthStatusGroupCardHarness[]> {
    return this.locatorForAll(HealthStatusGroupCardHarness)();
  }

  async getSchedulingPauseButton(): Promise<SchedulingPauseButtonHarness | null> {
    return this._pauseButton();
  }

  async getSchedulingPauseModal(): Promise<SchedulingPauseModalHarness | null> {
    return this._modal();
  }

  async hasSchedulingPauseCard(): Promise<boolean> {
    const btn = await this._pauseButton();
    return btn !== null;
  }

  async isSchedulingPauseCardPaused(): Promise<boolean> {
    const btn = await this._pauseButton();
    return btn ? btn.isPaused() : false;
  }

  async isPauseToggleChecked(): Promise<boolean> {
    const btn = await this._pauseButton();
    return btn ? btn.isChecked() : false;
  }

  async isPauseToggleDisabled(): Promise<boolean> {
    const btn = await this._pauseButton();
    return btn ? btn.isDisabled() : false;
  }

  async togglePause(): Promise<void> {
    const btn = await this._pauseButton();
    if (!btn) throw new Error('Pause toggle not found');
    return btn.toggle();
  }

  async hasConfirmationModal(): Promise<boolean> {
    const modal = await this._modal();
    return modal !== null;
  }

  async getModalMessageText(): Promise<string | null> {
    const modal = await this._modal();
    return modal ? modal.getMessageText() : null;
  }

  async clickModalConfirm(): Promise<void> {
    const modal = await this._modal();
    if (!modal) throw new Error('Modal confirm button not found');
    return modal.clickConfirm();
  }

  async clickModalCancel(): Promise<void> {
    const modal = await this._modal();
    if (!modal) throw new Error('Modal cancel button not found');
    return modal.clickCancel();
  }
}
