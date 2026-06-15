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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  inject,
  input,
  signal,
} from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { ListStatusMessagesResponse } from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import { LoadingBar } from 'xmanager_cloud/ui/app/common/components/loading_bar/loading_bar';
import { StatusMessage } from 'xmanager_cloud/ui/app/common/components/status_message/status_message';
import {
  StatusMessagesListFilter,
  TimeFilterValue,
  TimePreset,
} from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list_filter/status_messages_list_filter';
import {
  ALL_SEVERITIES,
  ALL_SOURCES,
} from 'xmanager_cloud/ui/app/service/status_message_filter_service';
import { StatusMessageService } from 'xmanager_cloud/ui/app/service/status_message_service';
import { combineLatest, EMPTY, Observable, Subject } from 'rxjs';
import {
  catchError,
  exhaustMap,
  filter,
  finalize,
  startWith,
  switchMap,
  tap,
} from 'rxjs/operators';

const DEFAULT_PAGE_SIZE = 25;

/**
 * Display a list of status messages for an experiment or work unit.
 */
@Component({
  selector: 'xmc-status-messages-list',
  standalone: true,
  templateUrl: './status_messages_list.ng.html',
  styleUrl: './status_messages_list.css',
  imports: [
    MatIconModule,
    MatProgressSpinnerModule,
    StatusMessage,
    MatButtonModule,
    LoadingBar,
    StatusMessagesListFilter,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusMessagesList {
  readonly parentName = input<string>('');
  private readonly statusMessageService = inject(StatusMessageService);
  private readonly destroyRef = inject(DestroyRef);

  readonly parent$ = toObservable(this.parentName);
  protected readonly isLoading = signal<boolean>(false);
  protected readonly loadingError = signal<string | undefined>(undefined);
  protected readonly messages = signal<StructuredMessage[]>([]);
  protected readonly nextPageToken = signal<string | undefined>(undefined);
  protected readonly showWorkUnitId = computed(() => !this.parentName().includes('/workUnits/'));

  readonly severityFilter = signal<readonly string[]>([...ALL_SEVERITIES]);
  readonly sourceFilter = signal<readonly string[]>([...ALL_SOURCES]);
  readonly sortOrder = signal<string>('DESC');
  readonly timeFilter = signal<TimeFilterValue>({ preset: TimePreset.ALL });
  private baseTimestamp: Date = new Date();

  private readonly severity$ = toObservable(this.severityFilter);
  private readonly source$ = toObservable(this.sourceFilter);
  private readonly sortOrder$ = toObservable(this.sortOrder);
  private readonly timeFilter$ = toObservable(this.timeFilter);

  private readonly loadMore$ = new Subject<void>();
  private readonly refreshTrigger$ = new Subject<void>();

  constructor() {
    combineLatest([
      this.parent$,
      this.severity$,
      this.source$,
      this.sortOrder$,
      this.timeFilter$,
      this.refreshTrigger$.pipe(startWith(undefined)),
    ])
      .pipe(
        filter(([parent]) => !!parent),
        switchMap(([parent]) => {
          this.messages.set([]);
          this.nextPageToken.set(undefined);
          // Base timestamp for calculating preset time ranges (e.g., "Last
          // 1 hour") to ensure the range is relative to the time the filter
          // was applied or refreshed.
          this.baseTimestamp = new Date();

          return this.loadMore$.pipe(
            startWith(undefined),
            filter(() => !this.isLoading()),
            filter(() => !this.messages().length || !!this.nextPageToken()),
            exhaustMap(() => this.fetchPage(parent, this.nextPageToken())),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
  }

  protected loadMore(): void {
    this.loadMore$.next();
  }

  protected refresh(): void {
    this.refreshTrigger$.next();
  }

  private fetchPage(parent: string, token?: string): Observable<ListStatusMessagesResponse> {
    this.isLoading.set(true);
    this.loadingError.set(undefined);

    const severity = this.severityFilter();
    const source = this.sourceFilter();
    if (severity.length === 0 || source.length === 0) {
      this.isLoading.set(false);
      return EMPTY;
    }

    const parts = [
      this.buildFilterClause('severity', severity, ALL_SEVERITIES.length),
      this.buildFilterClause('source', source, ALL_SOURCES.length),
      this.buildTimeFilterClause(),
    ].filter((p): p is string => !!p);
    const filterString = parts.join(' AND ') || undefined;

    const orderBy = `create_time ${this.sortOrder()}`;

    return this.statusMessageService
      .listStatusMessages({
        parent,
        pageSize: DEFAULT_PAGE_SIZE,
        pageToken: token,
        filter: filterString,
        orderBy,
      })
      .pipe(
        tap({
          next: (response: ListStatusMessagesResponse) => {
            const structuredMessages = (response.statusMessages || [])
              .map((msg) => msg.content)
              .filter((msg): msg is StructuredMessage => !!msg);
            this.messages.update((msgs) => [...msgs, ...structuredMessages]);
            this.nextPageToken.set(response.nextPageToken);
          },
          error: (err: { message?: string }) => {
            this.loadingError.set(err.message || 'Failed to load status messages');
          },
        }),
        finalize(() => this.isLoading.set(false)),
        catchError(() => EMPTY),
      );
  }

  private buildFilterClause(
    name: string,
    selected: readonly string[],
    allCount: number,
  ): string | undefined {
    if (selected.length === allCount) {
      return undefined;
    }
    return `(${selected.map((s) => `${name} = "${s}"`).join(' OR ')})`;
  }

  private buildTimeFilterClause(): string | undefined {
    const parts: string[] = [];
    const time = this.timeFilter();

    switch (time.preset) {
      case TimePreset.ALL:
        return undefined;
      case TimePreset.CUSTOM: {
        const startDate = time.start ? new Date(time.start) : null;
        if (startDate && !isNaN(startDate.getTime())) {
          parts.push(`create_time >= "${startDate.toISOString()}"`);
        }
        const endDate = time.end ? new Date(time.end) : null;
        if (endDate && !isNaN(endDate.getTime())) {
          parts.push(`create_time <= "${endDate.toISOString()}"`);
        }
        break;
      }
      case TimePreset.LAST_1_HOUR: {
        const filterDate = new Date(this.baseTimestamp);
        filterDate.setHours(filterDate.getHours() - 1);
        parts.push(`create_time >= "${filterDate.toISOString()}"`);
        break;
      }
      case TimePreset.LAST_24_HOURS: {
        const filterDate = new Date(this.baseTimestamp);
        filterDate.setHours(filterDate.getHours() - 24);
        parts.push(`create_time >= "${filterDate.toISOString()}"`);
        break;
      }
      case TimePreset.LAST_7_DAYS: {
        const filterDate = new Date(this.baseTimestamp);
        filterDate.setDate(filterDate.getDate() - 7);
        parts.push(`create_time >= "${filterDate.toISOString()}"`);
        break;
      }
      default:
        break;
    }

    return parts.length > 0 ? parts.join(' AND ') : undefined;
  }
}
