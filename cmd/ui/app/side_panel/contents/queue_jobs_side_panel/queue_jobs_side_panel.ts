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
  OnInit,
  signal,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatChipsModule } from '@angular/material/chips';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatTableModule } from '@angular/material/table';
import { RouterModule } from '@angular/router';

import { XidBadge } from 'xmanager_cloud/ui/app/common/components/xid_badge/xid_badge';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { QueueJobInfo } from 'xmanager_cloud/ui/app/service/resource_types';
import {
  BaseSidePanelContent,
  BaseSidePanelData,
} from 'xmanager_cloud/ui/app/side_panel/base_side_panel';
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';

/** Data for opening the Queue Jobs side panel. */
export class QueueJobsSidePanelData implements BaseSidePanelData {
  constructor(
    public queueName: string,
    public displayName?: string,
  ) {}
}

/** Side panel component to display pending and running jobs in a queue. */
@Component({
  selector: 'xmc-queue-jobs-side-panel',
  standalone: true,
  templateUrl: './queue_jobs_side_panel.ng.html',
  styleUrl: './queue_jobs_side_panel.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    FormsModule,
    RouterModule,
    MatButtonModule,
    MatButtonToggleModule,
    MatChipsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressSpinnerModule,
    MatTableModule,
    XidBadge,
  ],
})
export class QueueJobsSidePanel implements BaseSidePanelContent<QueueJobsSidePanelData>, OnInit {
  readonly data = input.required<QueueJobsSidePanelData>();

  private readonly resourceService = inject(ResourceService);
  private readonly sidePanelService = inject(SidePanelService);

  readonly isLoading = signal(true);
  readonly jobs = signal<QueueJobInfo[]>([]);
  readonly searchQuery = signal('');
  readonly statusFilter = signal('ALL');

  readonly filteredJobs = computed(() => {
    const query = this.searchQuery().toLowerCase().trim();
    const filter = this.statusFilter();

    return this.jobs().filter(
      (job) => matchesStatusFilter(job.status, filter) && matchesSearchQuery(job, query),
    );
  });

  readonly totalCount = computed(() => this.jobs().length);
  readonly runningCount = computed(
    () => this.jobs().filter((j) => matchesStatusFilter(j.status, 'RUNNING')).length,
  );
  readonly pendingCount = computed(
    () => this.jobs().filter((j) => matchesStatusFilter(j.status, 'PENDING')).length,
  );

  protected displayedColumns: string[] = [
    'position',
    'xid',
    'jobName',
    'user',
    'resources',
    'priority',
    'status',
    'timeInQueue',
  ];

  ngOnInit() {
    this.loadJobs();
  }

  loadJobs() {
    this.isLoading.set(true);
    const qName = this.data().queueName;
    this.resourceService.getQueueJobs(qName).subscribe({
      next: (resp) => {
        this.jobs.set(resp?.jobs || []);
        this.isLoading.set(false);
      },
      error: () => {
        this.isLoading.set(false);
      },
    });
  }

  close() {
    this.sidePanelService.closeSidePanel();
  }

  protected isNumericXid(xid: string): boolean {
    return /^\d+$/.test(xid);
  }
}

function matchesStatusFilter(status: string | undefined, filter: string): boolean {
  if (filter === 'ALL') {
    return true;
  }
  const statusUpper = (status || '').toUpperCase();
  if (filter === 'RUNNING') {
    return statusUpper === 'RUNNING' || statusUpper === 'ADMITTED';
  }
  if (filter === 'PENDING') {
    return statusUpper === 'PENDING' || statusUpper === 'RESERVING';
  }
  return false;
}

function matchesSearchQuery(job: QueueJobInfo, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields = [
    job.xid,
    job.workUnitId,
    job.jobName,
    job.user,
    job.priority,
    job.status,
    job.resources,
    job.position,
  ];
  return fields.some((field) => field?.toLowerCase().includes(query));
}
