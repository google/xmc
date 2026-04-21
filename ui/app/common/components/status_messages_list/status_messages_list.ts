import {ChangeDetectionStrategy, Component, DestroyRef, inject, input, signal} from '@angular/core';
import {takeUntilDestroyed, toObservable} from '@angular/core/rxjs-interop';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {MatProgressSpinnerModule} from '@angular/material/progress-spinner';
import {ListStatusMessagesResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {StructuredMessage} from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {LoadingBar} from 'xmanager_cloud/ui/app/common/components/loading_bar/loading_bar';
import {StatusMessage} from 'xmanager_cloud/ui/app/common/components/status_message/status_message';
import {StatusMessagesListFilter} from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list_filter/status_messages_list_filter';
import {ALL_SEVERITIES, ALL_SOURCES} from 'xmanager_cloud/ui/app/service/status_message_filter_service';
import {StatusMessageService} from 'xmanager_cloud/ui/app/service/status_message_service';
import {combineLatest, EMPTY, Observable, Subject} from 'rxjs';
import {catchError, exhaustMap, filter, finalize, startWith, switchMap, tap} from 'rxjs/operators';

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
  protected readonly loadingError = signal<string|undefined>(undefined);
  protected readonly messages = signal<StructuredMessage[]>([]);
  protected readonly nextPageToken = signal<string|undefined>(undefined);

  readonly severityFilter = signal<string[]>([...ALL_SEVERITIES]);
  readonly sourceFilter = signal<string[]>([...ALL_SOURCES]);
  readonly sortOrder = signal<string>('DESC');

  private readonly severity$ = toObservable(this.severityFilter);
  private readonly source$ = toObservable(this.sourceFilter);
  private readonly sortOrder$ = toObservable(this.sortOrder);

  private readonly loadMore$ = new Subject<void>();

  constructor() {
    combineLatest([this.parent$, this.severity$, this.source$, this.sortOrder$])
        .pipe(
            filter(([parent]) => !!parent), switchMap(([parent]) => {
              this.messages.set([]);
              this.nextPageToken.set(undefined);

              return this.loadMore$.pipe(
                  startWith(undefined), filter(() => !this.isLoading()),
                  filter(
                      () => !this.messages().length || !!this.nextPageToken()),
                  exhaustMap(
                      () => this.fetchPage(parent, this.nextPageToken())));
            }),
            takeUntilDestroyed(this.destroyRef))
        .subscribe();
  }

  protected loadMore(): void {
    this.loadMore$.next();
  }

  private fetchPage(parent: string, token?: string):
      Observable<ListStatusMessagesResponse> {
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
                this.messages.update(
                    (msgs) => [...msgs, ...(response.statusMessages || [])]);
                this.nextPageToken.set(response.nextPageToken);
              },
              error: (err: {message?: string}) => {
                this.loadingError.set(
                    err.message || 'Failed to load status messages');
              },
            }),
            finalize(() => this.isLoading.set(false)),
            catchError(() => EMPTY),
        );
  }

  private buildFilterClause(name: string, selected: string[], allCount: number):
      string|undefined {
    if (selected.length === allCount) {
      return undefined;
    }
    return `(${selected.map(s => `${name} = "${s}"`).join(' OR ')})`;
  }
}
