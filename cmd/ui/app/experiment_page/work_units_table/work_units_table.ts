// Copyright 2025 Google LLC
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
import { CommonModule, DatePipe } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  inject,
  input,
  Signal,
  untracked,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule } from '@angular/material/paginator';
import { MatSortModule, Sort } from '@angular/material/sort';
import { MatTableModule } from '@angular/material/table';
import { MatTooltip } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';

import { LoadingBar } from 'app/common/components/loading_bar/loading_bar';
import { PageSizeSelector } from 'app/common/components/page_size_selector/page_size_selector';
import { StatusChip, StatusOption } from 'app/common/components/status_chip/status_chip';
import {
  getStatusMessages,
  StatusMessage,
} from 'app/common/components/status_message/status_message';
import { StatusMessageFilter } from 'app/common/components/status_message_filter/status_message_filter';
import { Tag } from 'app/common/components/tag/tag';
import { ProcessedUrl, processUrls } from 'app/common/url/url_processor';
import { StructuredMessage, WorkUnit, WorkUnitStatus } from 'app/protos/experiment_state_server';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';

import { WorkUnitsTableStore } from './work_units_table_store';

enum WorkUnitColumn {
  ID = 'ID',
  STATUS = 'Status',
  TAGS = 'Tags',
  CREATED = 'Created',
  COMPLETED = 'Completed',
  URLS = 'Urls',
}

/** Maximum number of URLs to display in the table. */
const MAX_URLS_DISPLAYED = 5;

const GKE_DASHBOARD_DISPLAY_NAME = 'GKE Dashboard';

/** Interface for WorkUnit data prepared for template rendering. */
interface WorkUnitView extends WorkUnit {
  processedUrls: ProcessedUrl[];
  statusMessages: StructuredMessage[];
  isStatusMessageVisible: boolean;
  runProfileUrl?: string;
}

/**
 * Table of work units for a single experiment.
 */
@Component({
  selector: 'xmc-work-units-table',
  standalone: true,
  templateUrl: './work_units_table.ng.html',
  styleUrl: './work_units_table.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatTableModule,
    StatusChip,
    StatusMessage,
    MatPaginatorModule,
    DatePipe,
    MatIconModule,
    MatButtonModule,
    RouterLink,
    LoadingBar,
    Tag,
    PageSizeSelector,
    MatTooltip,
    StatusMessageFilter,
    MatSortModule,
  ],
  providers: [WorkUnitsTableStore],
})
export class WorkUnitsTable {
  readonly xid = input.required<string>();
  readonly disableWorkUnitTagLinks = input(true);
  readonly WorkUnitColumn = WorkUnitColumn;

  private readonly store = inject(WorkUnitsTableStore);
  private readonly filterService = inject(StatusMessageFilterService);

  protected readonly columnsToDisplay: readonly WorkUnitColumn[] = [
    WorkUnitColumn.ID,
    WorkUnitColumn.STATUS,
    WorkUnitColumn.TAGS,
    WorkUnitColumn.CREATED,
    WorkUnitColumn.COMPLETED,
    WorkUnitColumn.URLS,
  ];
  private readonly severities = toSignal(this.filterService.severities$);
  private readonly sources = toSignal(this.filterService.sources$);

  protected readonly workUnitViews: Signal<readonly WorkUnitView[] | undefined> = computed(() => {
    const workUnits = this.store.workUnits();
    if (workUnits === undefined) {
      return undefined;
    }
    this.severities();
    this.sources();
    return workUnits.map((workUnit) => {
      const status = workUnit.currentWorkUnitExecution?.status;
      const messages = getStatusMessages(status);

      let isStatusMessageVisible = false;
      if (messages.length > 0) {
        isStatusMessageVisible = messages.some((message) =>
          this.filterService.isMessageVisible(message),
        );
      }
      return {
        ...workUnit,
        processedUrls: processUrls(workUnit.urls ?? [], workUnit).slice(0, MAX_URLS_DISPLAYED),
        statusMessages: messages,
        isStatusMessageVisible,
        runProfileUrl: workUnit.currentWorkUnitExecution?.metadata?.mlrunUrl || undefined,
      };
    });
  });
  protected readonly pageSize: Signal<number> = this.store.pageSize;
  protected readonly pageIndex: Signal<number> = this.store.pageIndex;
  protected readonly hasNextPage: Signal<boolean> = this.store.hasNextPage;
  protected readonly hasPrevPage: Signal<boolean> = this.store.hasPrevPage;
  protected readonly isFetching: Signal<boolean> = this.store.isLoading;
  protected readonly error: Signal<string | null> = this.store.error;

  protected readonly isLoading: Signal<boolean> = computed(
    () => this.isFetching() && this.workUnitViews() === undefined,
  );

  protected readonly isRefreshing: Signal<boolean> = computed(
    () => this.isFetching() && this.workUnitViews() !== undefined,
  );

  protected readonly sort: Signal<Sort> = this.store.sort;

  constructor() {
    // The experiment page reuses this component across experiments, so reload whenever the
    // experiment id changes instead of only on init. Only `xid` is tracked.
    effect(() => {
      const xid = this.xid();
      untracked(() => {
        this.store.setXid(xid);
        this.store.fetchPage();
      });
    });
  }

  refresh() {
    this.store.fetchPage();
  }

  protected onSortChange(sort: Sort) {
    this.store.setSort(sort);
    this.store.fetchPage();
  }

  getStatusChip(status: WorkUnitStatus.WorkUnitState): StatusOption {
    switch (status) {
      case 'RUNNING':
        return StatusOption.RUNNING;
      case 'STARTING':
        return StatusOption.STARTING;
      case 'COMPLETED':
        return StatusOption.COMPLETED;
      case 'INITIALIZING':
        return StatusOption.INITIALIZING;
      case 'FAILED':
        return StatusOption.FAILED;
      case 'STOPPED':
        return StatusOption.STOPPED;
      case 'STOPPING':
        return StatusOption.STOPPING;
      case 'RESTARTING':
        return StatusOption.RESTARTING;
      case 'DESCHEDULING':
        return StatusOption.DESCHEDULING;
      case 'SCHEDULING':
        return StatusOption.SCHEDULING;
      case 'SCHEDULING_PAUSED':
        return StatusOption.SCHEDULING_PAUSED;
      default:
        return StatusOption.UNKNOWN;
    }
  }

  getWorkUnitId(name: string): string {
    // The name is in the format of experiments/{xid}/workUnits/{wid}
    return name.split('/').at(-1) ?? '';
  }

  protected isValidTime(time: string): boolean {
    if (!time) {
      return false;
    }
    return new Date(time).getTime() > 0;
  }

  nextPage() {
    this.store.goToNextPage();
    this.store.fetchPage();
  }

  prevPage() {
    this.store.goToPreviousPage();
    this.store.fetchPage();
  }

  onPageSizeChange(pageSize: number) {
    this.store.setPageSize(pageSize);
    this.store.fetchPage();
  }

  shouldDisableUrl(url: ProcessedUrl, workUnit: WorkUnitView): string | null {
    if (
      url.displayName === GKE_DASHBOARD_DISPLAY_NAME &&
      workUnit.currentWorkUnitExecution?.status?.state === 'STOPPED'
    ) {
      return 'There are no GKE resources for stopped work units';
    }
    return null;
  }
}
