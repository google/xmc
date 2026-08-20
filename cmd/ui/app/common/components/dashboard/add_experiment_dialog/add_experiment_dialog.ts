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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

export interface AddExperimentDialogResult {
  xid: number;
  wid?: number;
  title?: string;
}

/**
 * Dialog enabling researchers to enter XID and WID to add an experiment to dashboard comparison.
 */
@Component({
  selector: 'xmc-add-experiment-dialog',
  standalone: true,
  templateUrl: './add_experiment_dialog.ng.html',
  styleUrl: './add_experiment_dialog.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatButtonModule,
  ],
})
export class AddExperimentDialogComponent {
  private readonly dialogRef = inject(MatDialogRef<AddExperimentDialogComponent>);

  readonly form = new FormGroup({
    xid: new FormControl<number | null>(null, [Validators.required, Validators.min(1)]),
    wid: new FormControl<number | null>(null, [Validators.min(0)]),
    title: new FormControl<string>(''),
  });

  onCancel(): void {
    this.dialogRef.close();
  }

  onSubmit(): void {
    if (this.form.invalid) return;

    const val = this.form.value;
    const result: AddExperimentDialogResult = {
      xid: Number(val.xid),
      wid: val.wid != null ? Number(val.wid) : undefined,
      title: val.title?.trim() || undefined,
    };
    this.dialogRef.close(result);
  }
}
