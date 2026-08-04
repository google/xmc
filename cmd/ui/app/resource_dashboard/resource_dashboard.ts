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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  OnDestroy,
  OnInit,
  signal,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';

import {
  QueueOverview,
  QueueResourceCapacity,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';
import { QueueJobsTable } from 'xmanager_cloud/ui/app/side_panel/contents/queue_jobs_table/queue_jobs_table';

import {
  AutoReloadService,
  ReloaderControl,
} from '../common/components/autoreloader/autoreloader_service';
import { QueueListComponent } from './components/queue_list/queue_list';
import {
  ResourceRequirementQuery,
  SmartResourceFinderComponent,
} from './components/smart_resource_finder/smart_resource_finder';
import {
  DashboardTab,
  SubnavTabsComponent,
  TAB_RESOURCE_AVAILABILITY,
  TAB_SMART_FINDER,
} from './components/subnav_tabs/subnav_tabs';
import { ROD_TERM_TOOLTIPS } from './constants/rod_tooltips';

/**
 * Resource Observability Dashboard (ROD) primary layout container component.
 * Thin container orchestrating tab selection and cluster queue state.
 */
@Component({
  selector: 'xmc-resource-dashboard',
  standalone: true,
  templateUrl: './resource_dashboard.ng.html',
  styleUrl: './resource_dashboard.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    SubnavTabsComponent,
    SmartResourceFinderComponent,
    QueueListComponent,
    QueueJobsTable,
  ],
})
export class ResourceDashboard implements OnInit, OnDestroy {
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly route = inject(ActivatedRoute);
  private readonly resourceService = inject(ResourceService);

  private readonly clusterQueuesResponse = toSignal(this.resourceService.getClusterQueues());
  protected readonly clusterQueues = computed<ClusterQueue[]>(() => {
    const rawQueues: QueueOverview[] = this.clusterQueuesResponse()?.queues ?? [];
    return rawQueues.map((q) => {
      const name = q.queueName || 'cluster-queue';
      const rawCapacities: QueueResourceCapacity[] = q.queueCapacity || [];
      const flavors = rawCapacities.map((cap) => {
        const resourceType = cap.resourceType || 'resource';
        const totalCapacity = cap.totalCapacity ?? 0;
        const freeCapacity = cap.freeCapacity ?? 0;
        const inUse = Math.max(0, totalCapacity - freeCapacity);
        return {
          name: resourceType,
          resourceName: resourceType,
          nominalQuota: String(totalCapacity),
          inUse: String(inUse),
          reserved: '0',
          freeCapacity: String(freeCapacity),
          largestSlot: String(freeCapacity),
        };
      });

      return {
        name,
        strategy: 'BestEffortFIFO',
        cohort: '',
        status: 'Active',
        admittedWorkloads: 0,
        reservingWorkloads: 0,
        pendingWorkloads: 0,
        pendingJobsCount: 0,
        admittedJobsCount: 0,
        localQueues: [],
        flavors,
      };
    });
  });
  protected readonly isLoading = signal<boolean>(false);
  protected readonly selectedQueue = signal<string>('gpu-cluster-queue');
  protected readonly activeTab = signal<DashboardTab>(TAB_SMART_FINDER);
  protected readonly velocityPeriod = signal<string>('1h');
  protected readonly activeQuery = signal<ResourceRequirementQuery | null>(null);

  protected readonly TAB_SMART_FINDER = TAB_SMART_FINDER;
  protected readonly TAB_RESOURCE_AVAILABILITY = TAB_RESOURCE_AVAILABILITY;
  protected readonly termTooltips = ROD_TERM_TOOLTIPS;

  protected readonly availableResourceTypes = computed<string[]>(() => {
    const types = new Set<string>();
    for (const q of this.clusterQueues()) {
      for (const f of q.flavors ?? []) {
        if (f.resourceName) {
          types.add(f.resourceName);
        } else if (f.name) {
          types.add(f.name);
        }
      }
    }
    return Array.from(types).sort();
  });

  protected readonly filteredQueues = computed<ClusterQueue[]>(() => {
    const allQueues = this.clusterQueues();
    if (this.activeTab() !== TAB_SMART_FINDER) {
      return allQueues;
    }
    const query = this.activeQuery();
    if (!query) {
      return allQueues;
    }
    const reqAmount = query.amount;
    const activeFilters = query.resourceTypes
      .map((t) => t.trim().toLowerCase())
      .filter((t) => t !== '');

    return allQueues.filter((queue) => {
      const flavors = queue.flavors ?? [];
      if (!matchesResourceTypes(flavors, activeFilters)) {
        return false;
      }
      return matchesRequestedAmount(flavors, activeFilters, reqAmount);
    });
  });

  private readonly reloader: ReloaderControl = this.autoReloadService.createReloader({
    emitOnStart: false,
  });
  private readonly subscriptions = new Subscription();

  ngOnInit() {
    this.subscriptions.add(
      this.route.queryParams.subscribe((params) => {
        const queue = params['queue'];
        if (queue) {
          this.selectedQueue.set(queue);
        }
      }),
    );

    this.subscriptions.add(
      this.reloader.reloader$.subscribe(() => {
        // Auto-reload hook for periodic refresh
      }),
    );
    this.reloader.start();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
    this.reloader.stop();
  }

  protected onQueueSelect(queueName: string) {
    this.selectedQueue.set(queueName);
  }

  protected onTabChange(tab: DashboardTab) {
    this.activeTab.set(tab);
  }

  protected onFindMatchingQueues(query: ResourceRequirementQuery) {
    this.activeQuery.set(query);
  }
}

function matchesResourceTypes(flavors: ClusterQueue['flavors'], activeFilters: string[]): boolean {
  if (!activeFilters.length) return true;
  if (!flavors) return false;
  return activeFilters.every((filter) =>
    flavors.some((f) => {
      const rName = (f.resourceName || f.name || '').toLowerCase();
      return !!rName && (rName === filter || rName.includes(filter) || filter.includes(rName));
    }),
  );
}

function matchesRequestedAmount(
  flavors: ClusterQueue['flavors'],
  activeFilters: string[],
  reqAmount?: number | null,
): boolean {
  if (!reqAmount || reqAmount <= 0) return true;
  if (!flavors) return false;
  const targetFlavors =
    activeFilters.length > 0
      ? flavors.filter((f) => {
          const rName = (f.resourceName || f.name || '').toLowerCase();
          return (
            !!rName &&
            activeFilters.some(
              (filter) => rName === filter || rName.includes(filter) || filter.includes(rName),
            )
          );
        })
      : flavors;

  return targetFlavors.some((f) => {
    const free = parseFloat(f.freeCapacity || '0');
    return !isNaN(free) && free >= reqAmount;
  });
}
