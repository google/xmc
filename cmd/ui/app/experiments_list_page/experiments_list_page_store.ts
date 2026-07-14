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
import { inject, Injectable, Signal } from '@angular/core';
import { ComponentStore } from '@ngrx/component-store';
import { EMPTY } from 'rxjs';
import { catchError, switchMap, tap } from 'rxjs/operators';

import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {
  getInitialPaginationState,
  goToNextPage,
  goToPreviousPage,
  PaginationState,
  setPageSize,
} from 'xmanager_cloud/ui/app/common/pagination/pagination';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';

import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

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
export class ExperimentsListPageStore extends ComponentStore<ExperimentsListPageState> {
  private readonly service = inject(ExperimentService);
  private readonly snackbarService = inject(SnackbarService);


  constructor() {
    super(INITIAL_STATE);
  }

  readonly pageResult: Signal<api.ListExperimentsResponse | undefined> = this.selectSignal(
    (state) => state.pageResult,
  );

  readonly isLoading: Signal<boolean> = this.selectSignal((state) => state.isLoading);

  readonly pageIndex: Signal<number> = this.selectSignal((state) => state.pagination.pageIndex);

  readonly pageSize: Signal<number> = this.selectSignal((state) => state.pagination.pageSize);

  readonly hasNextPage: Signal<boolean> = this.selectSignal((state) =>
    Boolean(state.pagination.nextPageToken),
  );

  readonly hasPreviousPage: Signal<boolean> = this.selectSignal(
    (state) => state.pagination.previousTokens.length > 0,
  );

  /** Sets the filter. It does not fetch data. */
  readonly setFilter = this.updater((state, filter: string) => ({
    ...state,
    filter,
    pagination: getInitialPaginationState(state.pagination.pageSize),
  }));

  /** Sets the orderBy. It does not fetch data. */
  readonly setOrderBy = this.updater((state, orderBy: string) => ({
    ...state,
    orderBy,
    pagination: getInitialPaginationState(state.pagination.pageSize),
  }));

  /** Sets the page size. It does not fetch data. */
  readonly setPageSize = this.updater((state, pageSize: number) => setPageSize(state, pageSize));

  /** Goes to the next page. It does not fetch data. */
  readonly goToNextPage = this.updater((state) => goToNextPage(state));

  /** Goes to the previous page. It does not fetch data. */
  readonly goToPreviousPage = this.updater((state) => goToPreviousPage(state));

  /** Fetches the current page of experiments. */
  readonly fetchPage = this.effect((trigger$) =>
    trigger$.pipe(
      tap(() => this.patchState({ isLoading: true })),
      switchMap(() => {
        const { filter, pagination, orderBy } = this.get();
        const request: api.ListExperimentsRequest = {
          filter,
          pageSize: pagination.pageSize,
          pageToken: pagination.currentPageToken || undefined,
          orderBy,
        };
        return this.service.searchExperiments(request).pipe(
          tap((response) => this.patchState({
            isLoading: false,
            pageResult: response,
            pagination: {
              ...pagination,
              nextPageToken: response.nextPageToken,
            },
          })),
          catchError((err) => {
            const msg = err instanceof Error ? err.message : String(err);
            this.patchState({ isLoading: false });
            this.snackbarService.open(`Failed to fetch list of experiments: ${msg}`);
            return EMPTY;
          }),
        );
      }),
    ),
  );
}
