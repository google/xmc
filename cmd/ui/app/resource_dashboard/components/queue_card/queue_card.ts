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

import {
  FreeTotalDisplayPipe,
  MaxWorkloadDisplayPipe,
  PredictabilityMessagePipe,
  QueueStatusBadgePipe,
} from 'xmanager_cloud/ui/app/resource_dashboard/pipes/resource_dashboard_pipes';
import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';

@Component({
  selector: 'xmc-rod-queue-card',
  standalone: true,
  templateUrl: './queue_card.ng.html',
  styleUrl: './queue_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatIconModule,
    MaxWorkloadDisplayPipe,
    FreeTotalDisplayPipe,
    QueueStatusBadgePipe,
    PredictabilityMessagePipe,
  ],
})
export class QueueCardComponent {
  readonly queue = input.required<ClusterQueue>();
  readonly isSelected = input<boolean>(false);
  readonly selectQueue = output<string>();

  protected readonly flavors = computed(() => this.queue()?.flavors ?? []);

  protected onCardClick() {
    this.selectQueue.emit(this.queue().name);
  }
}
