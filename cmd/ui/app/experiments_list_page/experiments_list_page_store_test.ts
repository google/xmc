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
import { of, throwError } from 'rxjs';

import { ListExperimentsResponse } from 'app/protos/experiment_state_server';
import { ExperimentService } from 'app/service/experiment_service';
import { FeatureFlagService } from 'app/service/feature_flag_service';
import { HealthService } from 'app/service/health_service';
import { SnackbarService } from 'app/service/snackbar_service';
import { cleanState, get, setupModule, setupObservableMatchers } from 'app/testing/test';

import {
  ExperimentsListPageState,
  ExperimentsListPageStore,
  INITIAL_STATE,
} from './experiments_list_page_store';

describe('ExperimentsListPageStore', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(() => {
    const mockExperimentService = jasmine.createSpyObj<ExperimentService>('ExperimentService', [
      'searchExperiments',
    ]);
    const mockHealthService = jasmine.createSpyObj<HealthService>('HealthService', [
      'getSchedulingStatus',
    ]);
    mockHealthService.getSchedulingStatus.and.returnValue(of({ isSchedulingPaused: false }));

    let pauseSchedulingEnabled = false;
    const mockFeatureFlagService = {
      get isPauseSchedulingEnabled() {
        return pauseSchedulingEnabled;
      },
    };
    const mockSnackbarService = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);

    setupModule({
      providers: [
        ExperimentsListPageStore,
        { provide: ExperimentService, useValue: mockExperimentService },
        { provide: HealthService, useValue: mockHealthService },
        { provide: FeatureFlagService, useValue: mockFeatureFlagService },
        { provide: SnackbarService, useValue: mockSnackbarService },
      ],
    });

    const store = get(ExperimentsListPageStore);

    return {
      store,
      mockExperimentService,
      mockHealthService,
      mockSnackbarService,
      setPauseSchedulingEnabled: (val: boolean) => {
        pauseSchedulingEnabled = val;
      },
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
      state.mockExperimentService.searchExperiments.and.returnValue(of(mockResponse));

      state.store.fetchPage();

      expect(state.store.hasNextPage()).toBe(false);
    });
  });

  describe('setFilter()', () => {
    it('setFilter should update filter state', () => {
      const newFilter = 'user:me';
      state.store.setFilter(newFilter);

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
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

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
      expect(newState).toEqual({
        ...INITIAL_STATE,
        orderBy: newOrderBy,
      });
    });

    it('should reset pagination to page 1 when orderBy is set', () => {
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
  });

  describe('goToNextPage', () => {
    it('should update pagination state correctly when nextPageToken exists', () => {
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

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
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

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
      expect(newState).toEqual(initialState);
    });
  });

  describe('goToPreviousPage', () => {
    it('should update pagination state correctly when previousTokens exist', () => {
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

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
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

      expect(state.store.selectSignal((s: ExperimentsListPageState) => s)()).toEqual(INITIAL_STATE);
    });
  });

  describe('fetchPage', () => {
    const mockResponse: ListExperimentsResponse = {
      experiments: [{ name: 'experiments/1', title: 'Test Experiment 1' }],
      nextPageToken: 'page_2',
    };

    it('should call searchExperiments with correct parameters from state and update the state', () => {
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

      state.mockExperimentService.searchExperiments.and.returnValue(of(mockResponse));
      state.store.fetchPage();

      expect(state.mockExperimentService.searchExperiments).toHaveBeenCalledWith({
        filter,
        pageSize,
        pageToken: currentPageToken,
        orderBy: 'created desc',
      });

      const newState: ExperimentsListPageState = state.store.selectSignal(
        (s: ExperimentsListPageState) => s,
      )();
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

      expect(state.mockSnackbarService.open).toHaveBeenCalledWith(
        'Failed to fetch list of experiments: Some error',
      );
    });
  });

  describe('fetchSchedulingStatus', () => {
    it('should not call getSchedulingStatus and keep isSchedulingPaused false when feature flag is disabled', () => {
      state.setPauseSchedulingEnabled(false);
      state.store.patchState({ isSchedulingPaused: true });

      state.store.fetchSchedulingStatus();

      expect(state.mockHealthService.getSchedulingStatus).not.toHaveBeenCalled();
      expect(state.store.isSchedulingPaused()).toBe(false);
    });

    it('should set isSchedulingPaused to true when feature flag is enabled and scheduling is paused', () => {
      state.setPauseSchedulingEnabled(true);
      state.mockHealthService.getSchedulingStatus.and.returnValue(of({ isSchedulingPaused: true }));

      state.store.fetchSchedulingStatus();

      expect(state.mockHealthService.getSchedulingStatus).toHaveBeenCalled();
      expect(state.store.isSchedulingPaused()).toBe(true);
    });

    it('should log the error and fail open to false when getSchedulingStatus throws an error', () => {
      state.setPauseSchedulingEnabled(true);
      state.store.patchState({ isSchedulingPaused: true });
      const error = new Error('Network error');
      state.mockHealthService.getSchedulingStatus.and.returnValue(throwError(() => error));
      const consoleErrorSpy = spyOn(console, 'error');

      state.store.fetchSchedulingStatus();

      expect(state.store.isSchedulingPaused()).toBe(false);
      expect(consoleErrorSpy).toHaveBeenCalledWith('Failed to fetch scheduling status', error);
    });
  });
});
