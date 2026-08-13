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

import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  inject,
  OnDestroy,
  OnInit,
  signal,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';

import {
  ClusterQueueSecondaryMetrics,
  QueueOverview,
  QueueResourceCapacity,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { QueueJobsTable } from 'xmanager_cloud/ui/app/side_panel/contents/queue_jobs_table/queue_jobs_table';

import {
  AutoReloadService,
  ReloaderControl,
} from '../common/components/autoreloader/autoreloader_service';
import { WipBannerComponent } from '../common/components/wip_banner/wip_banner';
import { QueueListComponent } from './components/queue_list/queue_list';
import {
  ResourceRequirementQuery,
  ResourceRequirementRow,
  SmartResourceFinderComponent,
} from './components/smart_resource_finder/smart_resource_finder';
import {
  DashboardTab,
  SubnavTabsComponent,
  TAB_RESOURCE_AVAILABILITY,
  TAB_SMART_FINDER,
} from './components/subnav_tabs/subnav_tabs';
import { ROD_TERM_TOOLTIPS } from './constants/rod_tooltips';
import { mapPeriodToRangePreset } from './utils/resource_velocity';

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
    SubnavTabsComponent,
    SmartResourceFinderComponent,
    QueueListComponent,
    QueueJobsTable,
    WipBannerComponent,
  ],
})
export class ResourceDashboard implements OnInit, OnDestroy {
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly route = inject(ActivatedRoute);
  private readonly resourceService = inject(ResourceService);

  private readonly clusterQueuesResponse = toSignal(this.resourceService.getClusterQueues());
  protected readonly queues = computed<QueueOverview[]>(
    () => this.clusterQueuesResponse()?.queues ?? [],
  );
  protected readonly isLoading = signal<boolean>(false);
  protected readonly selectedQueue = signal<string>('gpu-cluster-queue');
  protected readonly activeTab = signal<DashboardTab>(TAB_SMART_FINDER);
  protected readonly velocityPeriod = signal<string>('1h');
  protected readonly activeQuery = signal<ResourceRequirementQuery | null>(null);
  protected readonly secondaryDataMap = signal<Record<string, ClusterQueueSecondaryMetrics>>({});
  protected readonly isSecondaryLoading = signal<boolean>(true);

  protected readonly TAB_SMART_FINDER = TAB_SMART_FINDER;
  protected readonly TAB_RESOURCE_AVAILABILITY = TAB_RESOURCE_AVAILABILITY;
  protected readonly termTooltips = ROD_TERM_TOOLTIPS;

  constructor() {
    effect(() => {
      const queues = this.queues();
      const period = this.velocityPeriod();
      if (queues.length > 0) {
        this.fetchSecondaryData();
      }
    });
  }

  protected readonly availableResourceTypes = computed<string[]>(() => {
    const types = new Set<string>();
    for (const q of this.queues()) {
      for (const cap of q.queueCapacity ?? []) {
        if (cap.resourceType) {
          types.add(cap.resourceType);
        }
      }
    }
    return Array.from(types).sort();
  });

  protected readonly activeResourceFilters = computed<string[]>(() => {
    const query = this.activeQuery();
    if (!query) return [];
    const types = new Set<string>();
    for (const req of query.requirements) {
      if (req.resourceType && req.resourceType.trim() !== '') {
        types.add(req.resourceType.trim());
      }
    }
    return Array.from(types);
  });

  protected readonly filteredQueues = computed<QueueOverview[]>(() => {
    const allQueues = this.queues();
    if (this.activeTab() !== TAB_SMART_FINDER) {
      return allQueues;
    }
    const query = this.activeQuery();
    if (!query || !query.requirements || query.requirements.length === 0) {
      return allQueues;
    }

    return allQueues.filter((queue) => {
      const capacities = queue.queueCapacity ?? [];
      return query.requirements.every((row) => matchesRequirementRow(capacities, row));
    });
  });

  private readonly reloader: ReloaderControl = this.autoReloadService.createReloader({
    emitOnStart: false,
  });
  private readonly subscriptions = new Subscription();
  private secondaryDataSub?: Subscription;

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
        this.fetchSecondaryData();
      }),
    );
    this.reloader.start();
  }

  protected fetchSecondaryData() {
    const queueNames = this.queues()
      .map((q) => q.queueName)
      .filter((name): name is string => !!name);
    if (!queueNames.length) {
      this.isSecondaryLoading.set(false);
      return;
    }
    this.isSecondaryLoading.set(true);
    this.secondaryDataSub?.unsubscribe();
    this.secondaryDataSub = this.resourceService
      .getQueueSecondaryData(queueNames, mapPeriodToRangePreset(this.velocityPeriod()))
      .subscribe({
        next: (res) => {
          this.secondaryDataMap.set(res.queuePredictability ?? {});
          this.isSecondaryLoading.set(false);
        },
        error: () => {
          this.isSecondaryLoading.set(false);
        },
      });
  }

  ngOnDestroy() {
    this.secondaryDataSub?.unsubscribe();
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

function matchesRequirementRow(
  capacities: QueueResourceCapacity[] | undefined,
  row: ResourceRequirementRow,
): boolean {
  if (!capacities) return false;
  const filter = (row.resourceType || '').trim().toLowerCase();
  const reqAmount = row.amount;

  return capacities.some((cap) => {
    const free = cap.freeCapacity ?? 0;
    if (free < reqAmount) return false;
    if (!filter) return true;

    const rName = (cap.resourceType || '').toLowerCase();
    return !!rName && (rName === filter || rName.includes(filter) || filter.includes(rName));
  });
}
