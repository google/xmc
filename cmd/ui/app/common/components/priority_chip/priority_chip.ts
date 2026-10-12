// Copyright 2025 Google LLC
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
import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatChipsModule } from '@angular/material/chips';

import { Priority } from 'app/protos/experiment_state_server';

/**
 * Component for displaying priority as a colored chip.
 */
@Component({
  selector: 'xmc-priority-chip',
  standalone: true,
  styleUrl: './priority_chip.scss',
  templateUrl: './priority_chip.ng.html',

  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PriorityChip {
  readonly priority = input.required<Priority | undefined>();
}
