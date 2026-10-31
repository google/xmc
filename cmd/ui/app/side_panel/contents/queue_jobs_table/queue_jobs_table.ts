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
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSelectModule } from '@angular/material/select';
import { MatSortModule, Sort } from '@angular/material/sort';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { catchError, distinctUntilChanged, of, switchMap, tap } from 'rxjs';

import { ResourceChip } from 'app/common/components/resource_chip/resource_chip';
import { StatusChip } from 'app/common/components/status_chip/status_chip';
import { ResourceService } from 'app/service/resource_service';
import { BaseSidePanelContent, BaseSidePanelData } from 'app/side_panel/base_side_panel';

import {
  QueueJobPositionPipe,
  QueueJobResourcesPipe,
  QueueJobStatusPipe,
} from './queue_jobs_pipes';

/** Payload data required to open the queue jobs inspection panel. */
export class QueueJobsTableData implements BaseSidePanelData {
  constructor(public readonly queueName: string) {}
}

/** Component presenting the list of active/pending jobs for a given cluster queue. */
@Component({
  selector: 'xmc-queue-jobs-table',
  standalone: true,
  templateUrl: './queue_jobs_table.ng.html',
  styleUrl: './queue_jobs_table.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    RouterModule,
    MatTableModule,
    MatSortModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
    MatPaginatorModule,
    MatIconModule,
    MatButtonModule,
    MatTooltipModule,
    StatusChip,
    ResourceChip,
    QueueJobPositionPipe,
    QueueJobStatusPipe,
    QueueJobResourcesPipe,
  ],
})
export class QueueJobsTable implements BaseSidePanelContent<QueueJobsTableData> {
  public readonly data = input<QueueJobsTableData>();
  public readonly queueName = input<string>('gpu-cluster-queue');

  protected readonly activeQueueName = computed(
    () => this.data()?.queueName || this.queueName() || 'gpu-cluster-queue',
  );

  private readonly resourceService = inject(ResourceService);

  protected readonly author = signal('');
  protected readonly status = signal('');
  protected readonly statuses = ['RUNNING', 'STARTING', 'INITIALIZING', 'SCHEDULING'];
  protected readonly filter = computed(() =>
    [
      this.author() ? `author = ${JSON.stringify(this.author())}` : '',
      this.status() ? `status = ${this.status()}` : '',
    ]
      .filter(Boolean)
      .join(' AND '),
  );
  protected readonly sort = signal<Sort>({ active: '', direction: '' });
  protected readonly orderBy = computed(() => {
    const sort = this.sort();
    return sort.direction ? `${sort.active} ${sort.direction}` : '';
  });

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
    orderBy: this.orderBy(),
    filter: this.filter(),
    pageSize: this.tableControls.pageSize(),
    pageToken: this.tableControls.pageToken(),
  }));

  protected readonly jobsResponse = toSignal(
    toObservable(this.requestParams).pipe(
      distinctUntilChanged(
        (prev, curr) =>
          prev.queueName === curr.queueName &&
          prev.pageSize === curr.pageSize &&
          prev.pageToken === curr.pageToken &&
          prev.orderBy === curr.orderBy &&
          prev.filter === curr.filter,
      ),
      tap(() => this.isLoading.set(true)),
      switchMap(({ queueName, pageSize, pageToken, orderBy, filter }) =>
        this.resourceService.getQueueJobs(queueName, pageSize, pageToken, orderBy, filter).pipe(
          tap((resp) => {
            this.isLoading.set(false);
            this.tableControls.totalCount.set(resp?.totalQueueSize ?? 0);
            this.tableControls.nextPageToken.set(resp?.nextPageToken ?? '');
            for (const job of resp?.jobs ?? []) {
              if (!job.user) {
                console.error(
                  `Job "${job.xid ? `Experiment #${job.xid}` : 'unknown'}" is missing an assigned user.`,
                );
              }
            }
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

  protected readonly pageSizeOptions = [5, 10, 20, 50] as const;

  protected readonly displayedColumns: readonly string[] = [
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
      untracked(() => this.resetPage());
    });
  }

  protected onSortChange(sort: Sort): void {
    this.sort.set(sort);
    this.resetPage();
  }

  private resetPage(): void {
    this.tableControls.pageIndex.set(0);
    this.tableControls.pageToken.set('');
    this.tableControls.pageTokens.set(['']);
  }

  protected onAuthorChange(author: string): void {
    this.author.set(author.trim());
    this.resetPage();
  }

  protected onStatusChange(status: string): void {
    this.status.set(status);
    this.resetPage();
  }

  protected clearFilters(): void {
    this.author.set('');
    this.status.set('');
    this.resetPage();
  }

  protected onPageChange(event: PageEvent): void {
    if (event.pageSize !== this.tableControls.pageSize()) {
      this.tableControls.pageSize.set(event.pageSize);
      this.resetPage();
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
