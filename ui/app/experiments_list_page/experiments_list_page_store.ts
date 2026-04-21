import {inject, Injectable, Signal} from '@angular/core';
import {ComponentStore} from '@ngrx/component-store';
import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {EMPTY} from 'rxjs';
import {catchError, switchMap, tap} from 'rxjs/operators';

import {getInitialPaginationState, PaginationState, setPageSize} from '../common/pagination/pagination';
import {ExperimentService} from '../service/experiment_service';
import {FeatureFlagService} from '../service/feature_flag_service';
import {SnackbarService} from '../service/snackbar_service';

/** State for the experiments list page. */
export interface ExperimentsListPageState {
  /** The filter used to fetch the current page of experiments. */
  filter?: string;

  /** The sort order used to fetch the current page of experiments. */
  orderBy: string;

  /** The pagination state used to fetch the current page of experiments. */
  pagination: PaginationState;

  /** Experiments list table data for the current page. */
  pageResult?: api.ListExperimentsResponse;

  /** Whether the experiments list table is loading. */
  isLoading: boolean;
}

/** Initial state for the models search page. */
export const INITIAL_STATE: ExperimentsListPageState = {
  orderBy: 'created desc',
  pagination: getInitialPaginationState(10),
  pageResult: undefined,
  isLoading: false,
};

/** Component store for the experiments list page. */
@Injectable()
export class ExperimentsListPageStore extends
    ComponentStore<ExperimentsListPageState> {
  private readonly service = inject(ExperimentService);
  private readonly snackbarService = inject(SnackbarService);
  private readonly featureFlagService = inject(FeatureFlagService);

  constructor() {
    super(INITIAL_STATE);
  }

  readonly pageResult: Signal<api.ListExperimentsResponse|undefined> =
      this.selectSignal((state) => state.pageResult);

  readonly isLoading: Signal<boolean> =
      this.selectSignal((state) => state.isLoading);

  readonly pageIndex: Signal<number> =
      this.selectSignal((state) => state.pagination.pageIndex);

  readonly pageSize: Signal<number> =
      this.selectSignal((state) => state.pagination.pageSize);

  readonly hasNextPage: Signal<boolean> =
      this.selectSignal((state) => Boolean(state.pagination.nextPageToken));

  readonly hasPreviousPage: Signal<boolean> =
      this.selectSignal((state) => state.pagination.previousTokens.length > 0);

  /** Sets the filter. It does not fetch data. */
  readonly setFilter = this.updater(
      (state, filter: string) => ({
        ...state,
        filter,
        pagination: getInitialPaginationState(state.pagination.pageSize),
      }));

  /** Sets the orderBy. It does not fetch data. */
  readonly setOrderBy = this.updater(
      (state, orderBy: string) => {
        const setPagination =
            this.featureFlagService.isCursorPaginationEnabled ?
            {pagination: getInitialPaginationState(state.pagination.pageSize)} :
            {};
        return {
          ...state,
          orderBy,
          ...setPagination,
        };
      },
  );

  /** Sets the page size. It does not fetch data. */
  readonly setPageSize =
      this.updater((state, pageSize: number) => setPageSize(state, pageSize));


  // TODO (kartonoe): This is copied from the work units table, we can refactor
  // it later.
  /** Goes to the next page. It does not fetch data. */
  readonly goToNextPage = this.updater((state) => {
    const {pagination} = state;

    if (!pagination.nextPageToken) {
      return state;
    }

    return {
      ...state,
      pagination: {
        ...pagination,
        pageIndex: pagination.pageIndex + 1,
        currentPageToken: pagination.nextPageToken,
        previousTokens:
            [...pagination.previousTokens, pagination.currentPageToken],
        nextPageToken: undefined,
      }
    };
  });

  /** Goes to the previous page. It does not fetch data. */
  readonly goToPreviousPage = this.updater((state) => {
    const {pagination} = state;
    const {previousTokens, pageIndex} = pagination;

    if (previousTokens.length === 0) {
      return state;
    }

    return {
      ...state,
      pagination: {
        ...pagination,
        currentPageToken: previousTokens[previousTokens.length - 1],
        previousTokens: previousTokens.slice(0, -1),
        pageIndex: pageIndex - 1,
      },
    };
  });

  /** Fetches the current page of experiments. */
  readonly fetchPage = this.effect(
      (trigger$) => {
        return trigger$.pipe(
            tap(() => {
              this.patchState({isLoading: true});
            }),
            switchMap(() => {
              const currentState = this.get();
              const paginationState = currentState.pagination;

              const request: api.ListExperimentsRequest = {
                filter: currentState.filter,
                pageSize: paginationState.pageSize,
                pageToken: paginationState.currentPageToken || undefined,
                orderBy: currentState.orderBy,
              };

              return this.service.searchExperiments(request).pipe(
                  tap(
                      (response: api.ListExperimentsResponse) => {
                        this.patchState({isLoading: false});
                        this.patchState({pageResult: response});

                        this.patchState({
                          pagination: {
                            ...paginationState,
                            nextPageToken: response.nextPageToken,
                          }
                        });
                      },
                      ),
                  catchError((error) => {
                    this.patchState({isLoading: false});
                    this.snackbarService.open(
                        `Failed to fetch list of experiments: ${
                            error.message || error}`);
                    return EMPTY;
                  }));
            }),
        );
      },
  );
}
