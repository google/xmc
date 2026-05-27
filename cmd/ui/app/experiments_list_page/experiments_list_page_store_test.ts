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
import 'jasmine';

import {ListExperimentsResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {of, throwError} from 'rxjs';

import {ExperimentService} from '../service/experiment_service';
import {FeatureFlagService} from '../service/feature_flag_service';
import {SnackbarService} from '../service/snackbar_service';

import {ExperimentsListPageState, ExperimentsListPageStore, INITIAL_STATE} from './experiments_list_page_store';

describe('ExperimentsListPageStore', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(() => {
    const mockExperimentService = jasmine.createSpyObj<ExperimentService>(
        'ExperimentService',
        ['searchExperiments'],
    );
    const mockSnackbarService = jasmine.createSpyObj<SnackbarService>(
        'SnackbarService',
        ['open'],
    );
    const mockFeatureFlagService = {
      isCursorPaginationEnabled: false,
    };

    setupModule({
      providers: [
        ExperimentsListPageStore,
        {provide: ExperimentService, useValue: mockExperimentService},
        {provide: SnackbarService, useValue: mockSnackbarService},
        {provide: FeatureFlagService, useValue: mockFeatureFlagService},
      ],
    });

    const store = get(ExperimentsListPageStore);

    return {
      store,
      mockExperimentService,
      mockSnackbarService,
      mockFeatureFlagService,
    };
  }, beforeEach);

  it('should be created', () => {
    expect(state.store).toBeTruthy();
  });

  describe('hasPreviousPage()', () => {
    it('should be true after going to next page', () => {
      state.store.patchState({
        pagination: {
          ...INITIAL_STATE.pagination,
          nextPageToken: 'nextToken',
        },
      });

      state.store.goToNextPage();

      expect(state.store.hasPreviousPage()).toBe(true);
    });
  });

  describe('hasNextPage()', () => {
    it('should be false after reaching the last page', () => {
      const mockResponse: ListExperimentsResponse = {
        experiments: [],
        nextPageToken: undefined,
      };
      state.mockExperimentService.searchExperiments.and.returnValue(
          of(mockResponse),
      );

      state.store.fetchPage();

      expect(state.store.hasNextPage()).toBe(false);
    });
  });

  describe('setFilter()', () => {
    it('setFilter should update filter state', () => {
      const newFilter = 'user:me';
      state.store.setFilter(newFilter);

      const newState: ExperimentsListPageState =
          state.store.selectSignal((state) => state)();
      expect(newState).toEqual({
        ...INITIAL_STATE,
        filter: newFilter,
      });
    });

    it('should reset pagination to page 1 when filter is set', () => {
      state.store.patchState({
        pagination: {
          ...INITIAL_STATE.pagination,
          nextPageToken: 'page2',
        },
      });
      state.store.goToNextPage();
      expect(state.store.pageIndex()).toBe(1);
      const newFilter = 'user:me';
      state.store.setFilter(newFilter);
      expect(state.store.pageIndex()).toBe(0);
      expect(state.store.hasPreviousPage()).toBe(false);
    });
  });

  describe('setOrderBy()', () => {
    it('setOrderBy should update orderBy state', () => {
      const newOrderBy = 'xid asc';
      state.store.setOrderBy(newOrderBy);

      const newState: ExperimentsListPageState =
          state.store.selectSignal((state) => state)();
      expect(newState).toEqual({
        ...INITIAL_STATE,
        orderBy: newOrderBy,
      });
    });

    it('should reset pagination to page 1 when orderBy is set and isCursorPaginationEnabled is true',
       () => {
         state.mockFeatureFlagService.isCursorPaginationEnabled = true;
         state.store.patchState({
           pagination: {
             ...INITIAL_STATE.pagination,
             nextPageToken: 'page2',
           },
         });
         state.store.goToNextPage();
         expect(state.store.pageIndex()).toBe(1);

         state.store.setOrderBy('xid asc');

         expect(state.store.pageIndex()).toBe(0);
         expect(state.store.hasPreviousPage()).toBe(false);
       });

    it('should not reset pagination to page 1 when orderBy is set and isCursorPaginationEnabled is false',
       () => {
         state.mockFeatureFlagService.isCursorPaginationEnabled = false;
         state.store.patchState({
           pagination: {
             ...INITIAL_STATE.pagination,
             nextPageToken: 'page2',
           },
         });
         state.store.goToNextPage();
         expect(state.store.pageIndex()).toBe(1);

         state.store.setOrderBy('xid asc');

         expect(state.store.pageIndex()).toBe(1);
         expect(state.store.hasPreviousPage()).toBe(true);
       });
  });

  describe('goToNextPage', () => {
    it('should update pagination state correctly when nextPageToken exists',
       () => {
         state.store.patchState({
           pagination: {
             currentPageToken: 'currToken',
             previousTokens: ['prevToken'],
             nextPageToken: 'nextToken',
             pageSize: 10,
             pageIndex: 1,
           },
         });

         state.store.goToNextPage();

         const newState: ExperimentsListPageState =
             state.store.selectSignal((state) => state)();
         expect(newState).toEqual({
           ...INITIAL_STATE,
           pagination: {
             ...INITIAL_STATE.pagination,
             currentPageToken: 'nextToken',
             previousTokens: ['prevToken', 'currToken'],
             nextPageToken: undefined,
             pageSize: 10,
             pageIndex: 2,
           },
         });
       });

    it('should not change state if no nextPageToken exists', () => {
      const initialState = {
        ...INITIAL_STATE,
        pagination: {
          ...INITIAL_STATE.pagination,
          currentPageToken: 'lastToken',
          nextPageToken: undefined,
          pageIndex: 2,
          previousTokens: ['', 'another'],
        },
      };
      state.store.setState(initialState);

      state.store.goToNextPage();

      const newState: ExperimentsListPageState =
          state.store.selectSignal((state) => state)();
      expect(newState).toEqual(initialState);
    });
  });

  describe('goToPreviousPage', () => {
    it('should update pagination state correctly when previousTokens exist',
       () => {
         const initialState = {
           ...INITIAL_STATE,
           pagination: {
             currentPageToken: 'currToken',
             previousTokens: ['prevToken1', 'prevToken2'],
             nextPageToken: undefined,
             pageSize: 10,
             pageIndex: 2,
           },
         };
         state.store.setState(initialState);

         state.store.goToPreviousPage();

         const newState: ExperimentsListPageState =
             state.store.selectSignal((state) => state)();
         expect(newState).toEqual({
           ...INITIAL_STATE,
           pagination: {
             ...INITIAL_STATE.pagination,
             currentPageToken: 'prevToken2',
             previousTokens: ['prevToken1'],
             nextPageToken: undefined,
             pageSize: 10,
             pageIndex: 1,
           },
         });
       });

    it('should not change state if no previousTokens exist', () => {
      state.store.setState(INITIAL_STATE);
      state.store.goToPreviousPage();

      expect(state.store.selectSignal((state) => state)())
          .toEqual(INITIAL_STATE);
    });
  });

  describe('fetchPage', () => {
    const mockResponse: ListExperimentsResponse = {
      experiments: [{name: 'experiments/1', title: 'Test Experiment 1'}],
      nextPageToken: 'page_2',
    };

    it('should call searchExperiments with correct parameters from state and update the state',
       () => {
         const filter = 'owner:test';
         const pageSize = 50;
         const currentPageToken = 'abc';
         state.store.patchState({
           filter,
           pagination: {
             ...INITIAL_STATE.pagination,
             pageSize,
             currentPageToken,
           },
         });

         state.mockExperimentService.searchExperiments.and.returnValue(
             of(mockResponse),
         );
         state.store.fetchPage();

         expect(
             state.mockExperimentService.searchExperiments,
             )
             .toHaveBeenCalledWith({
               filter,
               pageSize,
               pageToken: currentPageToken,
               orderBy: 'created desc',
             });

         const newState: ExperimentsListPageState =
             state.store.selectSignal((state) => state)();
         expect(newState).toEqual({
           ...INITIAL_STATE,
           filter,
           pagination: {
             ...INITIAL_STATE.pagination,
             pageSize,
             currentPageToken,
             nextPageToken: 'page_2',
           },
           pageResult: mockResponse,
           isLoading: false,
         });
       });

    it('should show a snackbar when searchExperiments throws an error', () => {
      state.mockExperimentService.searchExperiments.and.returnValue(
          throwError(() => new Error('Some error')),
      );
      state.store.fetchPage();

      expect(state.mockSnackbarService.open)
          .toHaveBeenCalledWith(
              'Failed to fetch list of experiments: Some error',
          );
    });
  });
});
