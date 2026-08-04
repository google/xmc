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
import { MatTooltipModule } from '@angular/material/tooltip';

import { ROD_TERM_TOOLTIPS } from 'xmanager_cloud/ui/app/resource_dashboard/constants/rod_tooltips';
import {
  FreeTotalDisplayPipe,
  MaxWorkloadDisplayPipe,
  QueueStatusBadgePipe,
} from 'xmanager_cloud/ui/app/resource_dashboard/pipes/resource_dashboard_pipes';
import { QueueOverview } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

@Component({
  selector: 'xmc-rod-queue-card',
  standalone: true,
  templateUrl: './queue_card.ng.html',
  styleUrl: './queue_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatIconModule,
    MatTooltipModule,
    MaxWorkloadDisplayPipe,
    FreeTotalDisplayPipe,
    QueueStatusBadgePipe,
  ],
})
export class QueueCardComponent {
  readonly queue = input.required<QueueOverview>();
  readonly isSelected = input<boolean>(false);
  readonly selectQueue = output<string>();

  // TODO: Restore copyFlag functionality and isCopied signal once flag formatting and requirements are finalized.
  protected readonly termTooltips = ROD_TERM_TOOLTIPS;
  protected readonly capacities = computed(() => this.queue()?.queueCapacity ?? []);

  protected readonly isQueueHealthy = computed(() => true);

  protected onCardClick(): void {
    const name = this.queue().queueName;
    if (name) {
      this.selectQueue.emit(name);
    }
  }
}
