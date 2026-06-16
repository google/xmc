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
import { cleanState, setupModule, get, setupObservableMatchers } from 'xmanager_cloud/ui/app/testing/test';
import 'jasmine';

import {UpdateExperimentRequest} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {NEVER, of, throwError} from 'rxjs';

import {EXPERIMENT_1, EXPERIMENT_2} from 'xmanager_cloud/ui/app/common/mock_data';
import {ExperimentService} from 'xmanager_cloud/ui/app/service/experiment_service';
import {SnackbarService} from 'xmanager_cloud/ui/app/service/snackbar_service';

import {EditExperimentFormStore} from './edit_experiment_form_store';

describe('EditExperimentFormStore', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(() => {
    const mockExperimentService = jasmine.createSpyObj<ExperimentService>(
        'ExperimentService',
        ['getExperiment', 'updateExperiment'],
    );
    const mockSnackbarService = jasmine.createSpyObj<SnackbarService>(
        'SnackbarService',
        ['open'],
    );

    setupModule({
      providers: [
        EditExperimentFormStore,
        {provide: ExperimentService, useValue: mockExperimentService},
        {provide: SnackbarService, useValue: mockSnackbarService},
      ],
    });

    return {
      store: get(EditExperimentFormStore),
      mockExperimentService,
      mockSnackbarService,
    };
  }, beforeEach);

  it('should be created with correct initial state', () => {
    expect(state.store).toBeDefined();
    expect(state.store.experiment()).toEqual(undefined);
    expect(state.store.isLoading()).toEqual(false);
  });

  describe('fetchExperiment', () => {
    const testXid = '123';

    it('should set isLoading to true while fetching', () => {
      state.mockExperimentService.getExperiment.and.returnValue(NEVER);

      state.store.fetchExperiment(testXid);

      expect(state.store.isLoading()).toBeTrue();
    });

    it('should update experiment and reset isLoading on success', () => {
      state.mockExperimentService.getExperiment.and.returnValue(
          of(EXPERIMENT_1),
      );

      state.store.fetchExperiment(testXid);

      expect(state.mockExperimentService.getExperiment)
          .toHaveBeenCalledWith(
              testXid,
          );
      expect(state.store.isLoading()).toBeFalse();
      expect(state.store.experiment()).toEqual(EXPERIMENT_1);
      expect(state.mockSnackbarService.open).not.toHaveBeenCalled();
    });

    it('should reset isLoading and show snackbar on error', () => {
      state.mockExperimentService.getExperiment.and.returnValue(
          throwError(() => new Error('Failed to load')),
      );

      state.store.fetchExperiment(testXid);

      expect(state.mockExperimentService.getExperiment)
          .toHaveBeenCalledWith(
              testXid,
          );
      expect(state.store.isLoading()).toBeFalse();
      expect(state.store.experiment()).toBeUndefined();
      expect(state.mockSnackbarService.open)
          .toHaveBeenCalledWith(
              'Failed to fetch experiment: Failed to load',
          );
    });
  });

  describe('updateExperiment', () => {
    const testRequest: UpdateExperimentRequest = {
      experiment: EXPERIMENT_1,
      updateMask: 'title,notes,tags,priority',
    };

    it('should set isLoading to true while updating', () => {
      state.mockExperimentService.updateExperiment.and.returnValue(NEVER);

      state.store.updateExperiment({request: testRequest});

      expect(state.store.isLoading()).toBeTrue();
    });

    it('should update experiment and reset isLoading on success', () => {
      state.mockExperimentService.updateExperiment.and.returnValue(
          of(EXPERIMENT_2),
      );

      state.store.updateExperiment({request: testRequest});

      expect(state.mockExperimentService.updateExperiment)
          .toHaveBeenCalledWith(testRequest);
      expect(state.store.isLoading()).toBeFalse();
      expect(state.store.experiment()).toEqual(EXPERIMENT_2);
    });

    it('should reset isLoading and show snackbar on error', () => {
      state.mockExperimentService.updateExperiment.and.returnValue(
          throwError(() => new Error('Failed to update')),
      );

      state.store.updateExperiment({request: testRequest});

      expect(state.mockExperimentService.updateExperiment)
          .toHaveBeenCalledWith(testRequest);
      expect(state.store.isLoading()).toBeFalse();
      expect(state.store.experiment()).toBeUndefined();
      expect(state.mockSnackbarService.open)
          .toHaveBeenCalledWith(
              'Failed to update experiment: Failed to update',
          );
    });
  });
});
