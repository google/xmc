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
import { take } from 'rxjs';

import {
  ClusterQueueSecondaryMetrics,
  QueueOverview,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { WipBannerComponent } from 'xmanager_cloud/ui/app/common/components/wip_banner/wip_banner';
import { ROD_TERM_TOOLTIPS } from 'xmanager_cloud/ui/app/resource_dashboard/constants/rod_tooltips';
import {
  CohortStateInfoPipe,
  FreeTotalDisplayPipe,
  MaxSchedulableDisplayPipe,
  QueueStatusBadgePipe,
} from 'xmanager_cloud/ui/app/resource_dashboard/pipes/resource_dashboard_pipes';
import { matchesResourceFilter } from 'xmanager_cloud/ui/app/resource_dashboard/utils/resource_filter';
import {
  calculateJobVelocity,
  calculatePendingRequestsDisplay,
  calculateResourceVelocity,
  getPendingJobsCount,
} from 'xmanager_cloud/ui/app/resource_dashboard/utils/resource_velocity';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';

export interface PredictabilityViewModel {
  readonly jobVelocityDisplay: string;
  readonly resourceVelocityDisplay: string;
  readonly pendingJobs: number;
  readonly requestedResourcesDisplay: string;
}

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
    MaxSchedulableDisplayPipe,
    CohortStateInfoPipe,
    FreeTotalDisplayPipe,
    QueueStatusBadgePipe,
    WipBannerComponent,
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
      .map((f) => f.trim())
      .filter((f) => f !== '');
    if (filters.length === 0) {
      return allCapacities;
    }
    return allCapacities.filter((cap) => {
      const rName = cap.resourceType || '';
      return !!rName && filters.some((f) => matchesResourceFilter(rName, f));
    });
  });

  // TODO: Restore actual health check logic once queue status/pending workloads are available in backend API.
  protected readonly isQueueHealthy = computed(() => true);

  protected readonly isCopied = signal<boolean>(false);
  protected readonly predictabilityVm = computed<PredictabilityViewModel | null>(() => {
    const sec = this.secondaryData();
    if (!sec?.queuePredictability?.length) return null;
    const reqRes = calculatePendingRequestsDisplay(sec);
    const firstPred = sec.queuePredictability[0];
    const resType = firstPred?.pendingResourceRequests?.resourceType;
    return {
      jobVelocityDisplay: calculateJobVelocity(sec, '1h'),
      resourceVelocityDisplay: calculateResourceVelocity(sec, '1h'),
      pendingJobs: getPendingJobsCount(sec),
      requestedResourcesDisplay: resType ? `${reqRes} ${resType}` : `${reqRes} units`,
    };
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

    this.resourceService
      .getSubmitQueue(clusterQueueName)
      .pipe(take(1))
      .subscribe((resp) => {
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
