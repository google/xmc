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

import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

import { UserResourceAllocation } from 'app/protos/rod';

import { summarizeUserAllocations } from './user_allocation_summary';

/**
 * Display-only stacked bar showing how much of the dominant resource each user requests
 * (running and pending) across all active jobs in the selected queue.
 */
@Component({
  selector: 'xmc-queue-user-allocation-bar',
  standalone: true,
  templateUrl: './queue_user_allocation_bar.ng.html',
  styleUrl: './queue_user_allocation_bar.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class QueueUserAllocationBar {
  readonly allocations = input<readonly UserResourceAllocation[]>([]);
  readonly dominantResource = input<string | null>(null);

  protected readonly summary = computed(() =>
    summarizeUserAllocations(this.allocations(), this.dominantResource()),
  );
}
