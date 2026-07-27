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
import { ChangeDetectionStrategy, Component, computed, input, output, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';

import { ROD_TERM_TOOLTIPS } from 'xmanager_cloud/ui/app/resource_dashboard/constants/rod_tooltips';
import {
  FreeTotalDisplayPipe,
  MaxWorkloadDisplayPipe,
  QueueStatusBadgePipe,
} from 'xmanager_cloud/ui/app/resource_dashboard/pipes/resource_dashboard_pipes';
import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';

const DEFAULT_CLUSTER_QUEUE: ClusterQueue = {
  name: 'gpu-cluster-queue',
  strategy: 'BestEffortFIFO',
  cohort: 'prod-gpu',
  status: 'Active',
  statusMessage: 'Can admit new workloads',
  lastTransitionTime: '2026-07-22T10:00:00Z',
  admittedWorkloads: 1,
  reservingWorkloads: 0,
  pendingWorkloads: 0,
  pendingJobsCount: 0,
  admittedJobsCount: 1,
  dominantResource: 'GPU H100',
  dominantResourceUnits: 8,
  requestedResources: '0',
  localQueues: [{ name: 'gpu-queue', namespace: 'default' }],
  flavors: [
    {
      name: 'h100-flavor',
      resourceName: 'nvidia.com/gpu',
      nominalQuota: '64',
      inUse: '8',
      reserved: '0',
      freeCapacity: '56',
      largestSlot: '56 → 64',
    },
  ],
};

@Component({
  selector: 'xmc-rod-queue-card',
  standalone: true,
  templateUrl: './queue_card.ng.html',
  styleUrl: './queue_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    MaxWorkloadDisplayPipe,
    FreeTotalDisplayPipe,
    QueueStatusBadgePipe,
  ],
})
export class QueueCardComponent {
  readonly queue = input<ClusterQueue>(DEFAULT_CLUSTER_QUEUE);
  readonly isSelected = input<boolean>(false);
  readonly selectQueue = output<string>();

  readonly isCopied = signal<boolean>(false);
  protected readonly termTooltips = ROD_TERM_TOOLTIPS;
  protected readonly flavors = computed(() => this.queue()?.flavors ?? []);

  protected readonly isQueueHealthy = computed(() => {
    const q = this.queue();
    if (!q || q.status !== 'Active') return false;
    return (q.pendingWorkloads || 0) === 0;
  });

  protected copyFlag(event: Event): void {
    event.stopPropagation();
    const qName = this.queue()?.name ?? '';
    const cleanedQueue = qName.replace(/-cluster-/g, '-');
    const flag = `--queue=${cleanedQueue}`;
    if (typeof navigator !== 'undefined' && navigator.clipboard) {
      navigator.clipboard
        .writeText(flag)
        .then(() => {
          this.isCopied.set(true);
          setTimeout(() => {
            this.isCopied.set(false);
          }, 2000);
        })
        .catch((err) => {
          console.error('Failed to copy queue flag to clipboard:', err);
        });
    }
  }

  protected onCardClick(): void {
    this.selectQueue.emit(this.queue().name);
  }
}
