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

import {
  getInitialPaginationState,
  PaginationState,
  setPageSize,
} from 'app/common/pagination/pagination';
import { ListWorkUnitsRequest, WorkUnit } from 'app/protos/experiment_state_server';
import { SnackbarService } from 'app/service/snackbar_service';
import { WorkUnitsService } from 'app/service/work_units_service';

import { AipSort, toOrderBy } from './work_units_query';

/** Interface for the state of the WorkUnitsTable. */
interface WorkUnitsTableState {
  xid: string | null;
  workUnits: readonly WorkUnit[] | undefined;
  pagination: PaginationState;
  isLoading: boolean;
  error: string | null;
  sort: AipSort;
}

/** Initial state for the WorkUnitsTableStore. */
const INITIAL_STATE: WorkUnitsTableState = {
  xid: null,
  workUnits: undefined,
  pagination: getInitialPaginationState(10),
  isLoading: false,
  error: null,
  sort: { active: '', direction: '' },
};

/**
 * Component store for the WorkUnitsTable.
 * Manages fetching and displaying work units with pagination.
 */
@Injectable()
export class WorkUnitsTableStore extends ComponentStore<WorkUnitsTableState> {
  private readonly workUnitsService = inject(WorkUnitsService);
  private readonly snackbarService = inject(SnackbarService);

  constructor() {
    super(INITIAL_STATE);
  }

  // SELECTORS
  readonly workUnits: Signal<readonly WorkUnit[] | undefined> = this.selectSignal(
    (state) => state.workUnits,
  );

  readonly isLoading: Signal<boolean> = this.selectSignal((state) => state.isLoading);

  readonly error: Signal<string | null> = this.selectSignal((state) => state.error);

  readonly pageSize: Signal<number> = this.selectSignal((state) => state.pagination.pageSize);

  readonly pageIndex: Signal<number> = this.selectSignal((state) => state.pagination.pageIndex);

  readonly hasNextPage: Signal<boolean> = this.selectSignal(
    (state) => !!state.pagination.nextPageToken,
  );

  readonly hasPrevPage: Signal<boolean> = this.selectSignal(
    (state) => state.pagination.previousTokens.length > 0,
  );

  readonly sort: Signal<AipSort> = this.selectSignal((state) => state.sort);

  readonly setXid = this.updater((state, xid: string) => ({
    ...state,
    xid,
  }));

  /** Sets the page size. It does not fetch data. */
  readonly setPageSize = this.updater((state, pageSize: number) => setPageSize(state, pageSize));

  /** Sets the sort and returns to the first page. It does not fetch data. */
  readonly setSort = this.updater((state, sort: AipSort) => ({
    ...state,
    sort,
    pagination: getInitialPaginationState(state.pagination.pageSize),
  }));

  /**
   * This updater will update page tokens:
   * - Assign the next page token to the current page token.
   * - Add the current page token to the previous tokens list.
   * - Set the next page token to null.
   */
  readonly goToNextPage = this.updater((state) => {
    const { pagination } = state;

    if (!pagination.nextPageToken) {
      return state;
    }

    return {
      ...state,
      pagination: {
        ...pagination,
        pageIndex: pagination.pageIndex + 1,
        currentPageToken: pagination.nextPageToken || pagination.currentPageToken,
        previousTokens: [...pagination.previousTokens, pagination.currentPageToken],
        nextPageToken: undefined,
      },
    };
  });

  /**
   * Update page tokens:
   * - Pop the last item from the previous tokens list and assign it to current
   * page token. The effect fetchPage will populate the nextPageToken,
   * and fetchPage is always called after goToPreviousPage.
   */
  readonly goToPreviousPage = this.updater((state) => {
    const { pagination } = state;
    const { previousTokens, pageIndex } = pagination;

    if (previousTokens.length === 0) {
      return state;
    }

    const newCurrentPageToken = previousTokens.at(-1) ?? '';
    const newPreviousTokens = previousTokens.slice(0, -1);

    return {
      ...state,
      pagination: {
        ...pagination,
        currentPageToken: newCurrentPageToken,
        previousTokens: newPreviousTokens,
        pageIndex: pageIndex - 1,
      },
    };
  });

  // EFFECTS
  /**
   * Fetch a page of work units with the current page token.
   * Will update the work units and next page token based on the response.
   */
  readonly fetchPage = this.effect((trigger$) =>
    trigger$.pipe(
      tap(() => this.patchState({ isLoading: true })),
      switchMap(() => {
        const { xid, pagination, sort } = this.get();
        const orderBy = toOrderBy(sort);
        const request: ListWorkUnitsRequest = {
          parent: `experiments/${xid}`,
          pageSize: pagination.pageSize,
          pageToken: pagination.currentPageToken,
          ...(orderBy && { orderBy }),
        };
        return this.workUnitsService.searchWorkUnits(request).pipe(
          tap((response) =>
            this.patchState({
              pagination: {
                ...pagination,
                nextPageToken: response.nextPageToken ?? undefined,
              },
              isLoading: false,
              workUnits: response.workUnits ?? [],
              error: null,
            }),
          ),
          catchError((err) => {
            const msg = err instanceof Error ? err.message : String(err);
            this.patchState({
              error: msg,
              isLoading: false,
              workUnits: [],
            });
            this.snackbarService.open(`Failed to fetch work units: ${msg}`);
            return EMPTY;
          }),
        );
      }),
    ),
  );
}
