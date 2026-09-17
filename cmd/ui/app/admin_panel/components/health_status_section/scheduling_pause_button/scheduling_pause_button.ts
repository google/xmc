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
import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import {
  MAT_SLIDE_TOGGLE_DEFAULT_OPTIONS,
  MatSlideToggleModule,
} from '@angular/material/slide-toggle';

/**
 * Component representing the scheduling pause/resume toggle card in the Admin Panel.
 */
@Component({
  selector: 'xmc-scheduling-pause-button',
  standalone: true,
  imports: [CommonModule, MatIconModule, MatSlideToggleModule],
  providers: [
    {
      provide: MAT_SLIDE_TOGGLE_DEFAULT_OPTIONS,
      useValue: {
        disableToggleValue: true,
      },
    },
  ],
  templateUrl: './scheduling_pause_button.ng.html',
  styleUrl: './scheduling_pause_button.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SchedulingPauseButtonComponent {
  isSchedulingPaused = input<boolean>(false);
  disabled = input<boolean>(false);

  toggleClick = output<void>();

  protected onToggleClick(): void {
    this.toggleClick.emit();
  }
}
