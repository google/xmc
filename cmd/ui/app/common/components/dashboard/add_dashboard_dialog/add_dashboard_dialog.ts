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
import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

/** Result returned when the add performance dashboard dialog is submitted. */
export interface AddDashboardDialogResult {
  readonly title: string;
  readonly xids: readonly number[];
}

/**
 * Popup modal prompted when clicking '+Add performance dashboard' on the dashboards tab.
 */
@Component({
  selector: 'xmc-add-dashboard-dialog',
  standalone: true,
  imports: [MatDialogModule, MatFormFieldModule, MatInputModule, MatButtonModule, FormsModule],
  templateUrl: './add_dashboard_dialog.ng.html',
  styleUrl: './add_dashboard_dialog.scss',
})
export class XmcAddDashboardDialogComponent {
  readonly dialogRef = inject(MatDialogRef<XmcAddDashboardDialogComponent>);

  readonly title = signal<string>('Performance Dashboard');
  readonly xidsInput = signal<string>('1');
  readonly errorMessage = signal<string | undefined>(undefined);

  /** Validates XID inputs and closes dialog with parsed numbers. */
  onSubmit(): void {
    const raw = this.xidsInput().trim();
    if (!raw) {
      this.errorMessage.set('Please enter at least one experiment XID.');
      return;
    }

    const xids = raw
      .split(',')
      .map((item) => parseInt(item.trim(), 10))
      .filter((num) => !isNaN(num) && num > 0);

    if (xids.length === 0) {
      this.errorMessage.set('Invalid XID format. Enter positive integers separated by commas.');
      return;
    }

    const result: AddDashboardDialogResult = {
      title: this.title().trim() || 'Performance Dashboard',
      xids,
    };
    this.dialogRef.close(result);
  }

  onCancel(): void {
    this.dialogRef.close(undefined);
  }
}
