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
  viewChild,
} from '@angular/core';
import { toObservable, toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginator, MatPaginatorModule } from '@angular/material/paginator';
import { MatTableDataSource, MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { catchError, map, of, switchMap, tap } from 'rxjs';

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

  protected readonly jobs = toSignal(
    toObservable(this.activeQueueName).pipe(
      tap(() => this.isLoading.set(true)),
      switchMap((qName) =>
        this.resourceService.getQueueJobs(qName).pipe(
          map((resp) => resp?.jobs ?? []),
          tap(() => this.isLoading.set(false)),
          catchError(() => {
            this.isLoading.set(false);
            return of([]);
          }),
        ),
      ),
    ),
    { initialValue: [] },
  );

  protected readonly totalCount = computed(() => this.jobs().length);

  protected readonly dataSource = new MatTableDataSource<DashboardJob>();
  readonly matPaginator = viewChild(MatPaginator);

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
      this.dataSource.data = this.jobs();
    });
    effect(() => {
      this.dataSource.paginator = this.matPaginator() ?? null;
    });
  }
}
