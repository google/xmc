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
  input,
  OnChanges,
  OnDestroy,
  OnInit,
  signal,
  SimpleChanges,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { Subscription } from 'rxjs';

import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { QueueJobInfo } from 'xmanager_cloud/ui/app/service/resource_types';
import {
  BaseSidePanelContent,
  BaseSidePanelData,
} from 'xmanager_cloud/ui/app/side_panel/base_side_panel';

/** Payload data required to open the queue jobs inspection panel. */
export class QueueJobsSidePanelData implements BaseSidePanelData {
  constructor(readonly queueName: string) {}
}

/**
 * Modern Angular standalone component displaying pending and running jobs for a selected queue.
 */
@Component({
  selector: 'xmc-queue-jobs-side-panel',
  standalone: true,
  templateUrl: './queue_jobs_side_panel.ng.html',
  styleUrl: './queue_jobs_side_panel.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    FormsModule,
    RouterModule,
    MatTableModule,
    MatIconModule,
    MatButtonModule,
    MatTooltipModule,
  ],
})
export class QueueJobsSidePanel
  implements OnInit, OnChanges, OnDestroy, BaseSidePanelContent<QueueJobsSidePanelData>
{
  private readonly resourceService = inject(ResourceService);
  private jobsSubscription?: Subscription;

  readonly data = input<QueueJobsSidePanelData>();
  readonly queueName = input<string>('gpu-cluster-queue');

  readonly activeQueueName = computed(
    () => this.data()?.queueName || this.queueName() || 'gpu-cluster-queue',
  );

  protected readonly jobs = signal<QueueJobInfo[]>([]);
  protected readonly isLoading = signal<boolean>(false);
  protected readonly filterQuery = signal<string>('');
  protected readonly pageIndex = signal<number>(0);
  protected readonly pageSize = signal<number>(10);
  protected readonly pageSizeOptions = [5, 10, 25];

  protected readonly filteredJobs = computed(() => {
    const query = this.filterQuery().trim().toLowerCase();
    const all = this.jobs();
    if (!query) return all;
    return all.filter((job) => matchesSearchQuery(job, query));
  });

  protected readonly pagedJobs = computed(() => {
    const start = this.pageIndex() * this.pageSize();
    return this.filteredJobs().slice(start, start + this.pageSize());
  });

  protected readonly totalCount = computed(() => this.jobs().length);

  protected displayedColumns: string[] = [
    'position',
    'jobName',
    'status',
    'priority',
    'workUnitId',
    'user',
  ];

  ngOnInit() {
    this.loadJobs();
  }

  ngOnChanges(changes: SimpleChanges) {
    if (!changes['queueName']?.isFirstChange() && !changes['data']?.isFirstChange()) {
      this.loadJobs();
    }
  }

  ngOnDestroy() {
    this.jobsSubscription?.unsubscribe();
  }

  protected loadJobs() {
    const qName = this.activeQueueName();
    if (!qName) return;
    this.jobsSubscription?.unsubscribe();
    this.isLoading.set(true);
    this.jobsSubscription = this.resourceService.getQueueJobs(qName).subscribe({
      next: (resp) => {
        this.jobs.set(resp?.jobs ?? []);
        this.isLoading.set(false);
      },
      error: () => {
        this.jobs.set([]);
        this.isLoading.set(false);
      },
    });
  }

  protected onFilterQueryChange(query: string) {
    this.filterQuery.set(query);
    this.pageIndex.set(0);
  }

  protected onPageSizeChange(newSize: string | number) {
    const size = typeof newSize === 'string' ? parseInt(newSize, 10) : newSize;
    if (!isNaN(size) && size > 0) {
      this.pageSize.set(size);
      this.pageIndex.set(0);
    }
  }

  protected getMaxPageIndex(): number {
    const total = this.filteredJobs().length;
    return Math.max(0, Math.ceil(total / this.pageSize()) - 1);
  }

  protected goToFirstPage() {
    this.pageIndex.set(0);
  }

  protected goToPreviousPage() {
    this.pageIndex.update((idx) => Math.max(0, idx - 1));
  }

  protected goToNextPage() {
    this.pageIndex.update((idx) => Math.min(this.getMaxPageIndex(), idx + 1));
  }

  protected goToLastPage() {
    this.pageIndex.set(this.getMaxPageIndex());
  }

  protected getPaginationRangeDisplay(): string {
    const total = this.filteredJobs().length;
    if (total === 0) return '0-0 of 0';
    const start = this.pageIndex() * this.pageSize() + 1;
    const end = Math.min(total, (this.pageIndex() + 1) * this.pageSize());
    return `${start}-${end} of ${total}`;
  }

  protected getPriorityDisplay(priority?: string): string {
    if (!priority) return 'Normal';
    const p = priority.toLowerCase();
    if (p.startsWith('high')) return 'High';
    if (p.startsWith('low')) return 'Low';
    return priority.charAt(0).toUpperCase() + priority.slice(1);
  }

  protected getPriorityBadgeClass(priority?: string): string {
    const formatted = this.getPriorityDisplay(priority).toLowerCase();
    return `priority-${formatted}`;
  }
}

function matchesSearchQuery(job: QueueJobInfo, query: string): boolean {
  const fields = [
    job.jobName,
    job.user,
    job.status,
    job.priority,
    job.xid,
    job.workUnitId,
    job.resources,
    job.position,
  ];
  return fields.some((field) => field?.toLowerCase()?.includes(query) ?? false);
}
