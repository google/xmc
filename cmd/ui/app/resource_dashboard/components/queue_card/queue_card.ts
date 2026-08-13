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

import { Clipboard } from '@angular/cdk/clipboard';
import { DecimalPipe } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  output,
  signal,
} from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatTooltipModule } from '@angular/material/tooltip';

import { QueueOverview, QueuePredictability, ClusterQueueSecondaryMetrics } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ROD_TERM_TOOLTIPS } from 'xmanager_cloud/ui/app/resource_dashboard/constants/rod_tooltips';
import {
  FreeTotalDisplayPipe,
  MaxWorkloadDisplayPipe,
  QueueStatusBadgePipe,
} from 'xmanager_cloud/ui/app/resource_dashboard/pipes/resource_dashboard_pipes';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';

@Component({
  selector: 'xmc-rod-queue-card',
  standalone: true,
  templateUrl: './queue_card.ng.html',
  styleUrl: './queue_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    DecimalPipe,
    MatIconModule,
    MatProgressSpinnerModule,
    MatTooltipModule,
    MaxWorkloadDisplayPipe,
    FreeTotalDisplayPipe,
    QueueStatusBadgePipe,
  ],
})
export class QueueCardComponent {
  readonly queue = input.required<QueueOverview>();
  readonly secondaryData = input<ClusterQueueSecondaryMetrics | undefined>(undefined);
  readonly isSecondaryLoading = input<boolean>(false);
  readonly isSelected = input<boolean>(false);
  readonly activeResourceFilters = input<string[]>([]);
  readonly selectQueue = output<string>();

  private readonly clipboard = inject(Clipboard);
  private readonly resourceService = inject(ResourceService);

  protected readonly termTooltips = ROD_TERM_TOOLTIPS;
  protected readonly capacities = computed(() => {
    const allCapacities = this.queue()?.queueCapacity ?? [];
    const filters = this.activeResourceFilters()
      .map((f) => f.trim().toLowerCase())
      .filter((f) => f !== '');
    if (filters.length === 0) {
      return allCapacities;
    }
    return allCapacities.filter((cap) => {
      const rName = (cap.resourceType || '').toLowerCase();
      return !!rName && filters.some((f) => rName === f || rName.includes(f) || f.includes(rName));
    });
  });

  // TODO: Restore actual health check logic once queue status/pending workloads are available in backend API.
  protected readonly isQueueHealthy = computed(() => true);

  protected readonly isCopied = signal<boolean>(false);
  protected readonly predictability = computed<QueuePredictability | undefined>(() => {
    const sec = this.secondaryData();
    return sec?.queuePredictability?.[0];
  });

  protected onCardClick(): void {
    const name = this.queue().queueName;
    if (name) {
      this.selectQueue.emit(name);
    }
  }

  protected onCopyFlag(event: MouseEvent): void {
    event.stopPropagation();
    const clusterQueueName = this.queue()?.queueName ?? '';
    if (!clusterQueueName) return;

    this.resourceService.getSubmitQueue(clusterQueueName).subscribe((resp) => {
      const targetQueue = resp?.submitQueue || clusterQueueName;
      const flagText = `--queue=${targetQueue}`;

      if (this.clipboard.copy(flagText)) {
        this.isCopied.set(true);
        setTimeout(() => {
          this.isCopied.set(false);
        }, 2000);
      }
    });
  }
}
