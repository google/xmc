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
  input,
  signal,
  untracked,
} from '@angular/core';
import { toObservable, toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatTableDataSource, MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { catchError, of, switchMap, tap } from 'rxjs';

import { DashboardJob } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceChip } from 'xmanager_cloud/ui/app/common/components/resource_chip/resource_chip';
import { StatusChip } from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';
import { WipBannerComponent } from 'xmanager_cloud/ui/app/common/components/wip_banner/wip_banner';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import {
  BaseSidePanelContent,
  BaseSidePanelData,
} from 'xmanager_cloud/ui/app/side_panel/base_side_panel';

import {
  QueueJobPositionPipe,
  QueueJobResourcesPipe,
  QueueJobStatusPipe,
} from './queue_jobs_pipes';

/** Payload data required to open the queue jobs inspection panel. */
export class QueueJobsTableData implements BaseSidePanelData {
  constructor(readonly queueName: string) {}
}

/**
 * Modern Angular standalone component displaying pending and running jobs for a selected queue.
 */
@Component({
  selector: 'xmc-queue-jobs-table',
  standalone: true,
  templateUrl: './queue_jobs_table.ng.html',
  styleUrl: './queue_jobs_table.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    RouterModule,
    MatTableModule,
    MatPaginatorModule,
    MatIconModule,
    MatButtonModule,
    MatTooltipModule,
    StatusChip,
    ResourceChip,
    QueueJobStatusPipe,
    QueueJobPositionPipe,
    QueueJobResourcesPipe,
    WipBannerComponent,
  ],
})
export class QueueJobsTable implements BaseSidePanelContent<QueueJobsTableData> {
  private readonly resourceService = inject(ResourceService);

  readonly data = input<QueueJobsTableData>();
  readonly queueName = input<string>('gpu-cluster-queue');

  readonly activeQueueName = computed(
    () => this.data()?.queueName || this.queueName() || 'gpu-cluster-queue',
  );

  protected readonly isLoading = signal<boolean>(false);
  protected readonly tableControls = {
    pageSize: signal<number>(10),
    pageIndex: signal<number>(0),
    pageToken: signal<string>(''),
    pageTokens: signal<string[]>(['']),
    totalCount: signal<number>(0),
    nextPageToken: signal<string>(''),
  };

  protected readonly requestParams = computed(() => ({
    queueName: this.activeQueueName(),
    pageSize: this.tableControls.pageSize(),
    pageToken: this.tableControls.pageToken(),
  }));

  protected readonly jobsResponse = toSignal(
    toObservable(this.requestParams).pipe(
      tap(() => this.isLoading.set(true)),
      switchMap(({ queueName, pageSize, pageToken }) =>
        this.resourceService.getQueueJobs(queueName, pageSize, pageToken).pipe(
          tap((resp) => {
            this.isLoading.set(false);
            this.tableControls.totalCount.set(resp?.totalQueueSize ?? 0);
            this.tableControls.nextPageToken.set(resp?.nextPageToken ?? '');
          }),
          catchError(() => {
            this.isLoading.set(false);
            this.tableControls.totalCount.set(0);
            this.tableControls.nextPageToken.set('');
            return of({ jobs: [], totalQueueSize: 0, nextPageToken: '' });
          }),
        ),
      ),
    ),
    { initialValue: { jobs: [], totalQueueSize: 0, nextPageToken: '' } },
  );

  protected readonly jobs = computed(() => this.jobsResponse()?.jobs ?? []);

  protected readonly dataSource = new MatTableDataSource<DashboardJob>();

  protected readonly pageSizeOptions = [5, 10, 20, 50] as const;

  protected displayedColumns: string[] = [
    'position',
    'jobName',
    'status',
    'priority',
    'resources',
    'workUnitId',
    'user',
  ];

  constructor() {
    effect(() => {
      this.activeQueueName();
      untracked(() => {
        this.tableControls.pageIndex.set(0);
        this.tableControls.pageToken.set('');
        this.tableControls.pageTokens.set(['']);
      });
    });

    effect(() => {
      this.dataSource.data = this.jobs();
    });
  }

  protected onPageChange(event: PageEvent): void {
    if (event.pageSize !== this.tableControls.pageSize()) {
      this.tableControls.pageSize.set(event.pageSize);
      this.tableControls.pageIndex.set(0);
      this.tableControls.pageToken.set('');
      this.tableControls.pageTokens.set(['']);
      return;
    }

    const newIndex = event.pageIndex;
    const oldIndex = this.tableControls.pageIndex();

    if (newIndex === oldIndex) {
      return;
    }

    let token = '';
    if (newIndex > oldIndex) {
      token = this.tableControls.nextPageToken();
    } else {
      token = this.tableControls.pageTokens()[newIndex] ?? '';
    }

    const updatedTokens = [...this.tableControls.pageTokens()];
    updatedTokens[newIndex] = token;
    this.tableControls.pageTokens.set(updatedTokens);
    this.tableControls.pageIndex.set(newIndex);
    this.tableControls.pageToken.set(token);
  }
}
