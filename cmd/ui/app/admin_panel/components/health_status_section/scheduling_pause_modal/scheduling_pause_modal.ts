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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input, output } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

/**
 * Confirmation modal dialog for pausing or resuming scheduling.
 */
@Component({
  selector: 'xmc-scheduling-pause-modal',
  standalone: true,
  imports: [CommonModule, MatIconModule],
  templateUrl: './scheduling_pause_modal.ng.html',
  styleUrl: './scheduling_pause_modal.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  host: {
    '(window:keydown.escape)': 'onCancel()',
  },
})
export class SchedulingPauseModalComponent {
  /** True if the impending action is pausing scheduling; false if resuming. */
  isPausing = input.required<boolean>();

  confirm = output<void>();
  cancel = output<void>();

  protected readonly confirmButtonText = computed(() =>
    this.isPausing() ? 'Yes, Pause' : 'Yes, Resume',
  );

  protected onConfirm(): void {
    this.confirm.emit();
  }

  protected onCancel(): void {
    this.cancel.emit();
  }

  protected onBackdropClick(): void {
    this.cancel.emit();
  }
}
