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

/** Component harness for AddExperimentDialogComponent. */
export class AddExperimentDialogHarness extends ComponentHarness {
  static hostSelector = 'xmc-add-experiment-dialog';

  private readonly submitButton = this.locatorFor('mat-dialog-actions button[color="primary"]');
  private readonly cancelButton = this.locatorFor(
    'mat-dialog-actions button:not([color="primary"])',
  );

  async getSubmitButtonText(): Promise<string> {
    return (await this.submitButton()).text();
  }

  async isSubmitButtonDisabled(): Promise<boolean> {
    const btn = await this.submitButton();
    return btn.getProperty<boolean>('disabled');
  }

  async clickSubmit(): Promise<void> {
    const btn = await this.submitButton();
    await btn.click();
  }

  async clickCancel(): Promise<void> {
    const btn = await this.cancelButton();
    await btn.click();
  }
}
