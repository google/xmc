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
import { UpdateExperimentRequest } from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { EMPTY, Observable } from 'rxjs';
import { catchError, finalize, switchMap, tap } from 'rxjs/operators';

import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

/** State for the Edit Experiment form. */
interface EditExperimentFormState {
  experiment?: Experiment;
  isLoading: boolean;
}

/** Initial state for the Edit Experiment form. */
const INITIAL_STATE: EditExperimentFormState = {
  experiment: undefined,
  isLoading: false,
};

/** Component store for the Edit Experiment form. */
@Injectable()
export class EditExperimentFormStore extends ComponentStore<EditExperimentFormState> {
  private readonly experimentService = inject(ExperimentService);
  private readonly snackbarService = inject(SnackbarService);

  constructor() {
    super(INITIAL_STATE);
  }

  readonly experiment: Signal<Experiment | undefined> = this.selectSignal(
    (state) => state.experiment,
  );

  readonly isLoading: Signal<boolean> = this.selectSignal((state) => state.isLoading);

  readonly fetchExperiment: (xid: string) => void = this.effect((xid$: Observable<string>) => {
    return xid$.pipe(
      switchMap((xid: string) => {
        this.patchState({ isLoading: true });
        return this.experimentService.getExperiment(xid).pipe(
          tap((experiment) => {
            this.patchState({ experiment, isLoading: false });
          }),
          catchError((error) => {
            this.patchState({ isLoading: false });
            this.snackbarService.open(`Failed to fetch experiment: ${error.message || error}`);
            return EMPTY;
          }),
        );
      }),
    );
  });

  readonly updateExperiment = this.effect<{
    request: UpdateExperimentRequest;
    onSuccess?: () => void;
    onFinish?: () => void;
  }>((obs$) => {
    return obs$.pipe(
      switchMap(({ request, onSuccess, onFinish }) => {
        this.patchState({ isLoading: true });
        return this.experimentService.updateExperiment(request).pipe(
          tap((experiment) => {
            this.patchState({ experiment, isLoading: false });
            this.snackbarService.open(`Experiment updated.`);
            if (onSuccess) {
              onSuccess();
            }
          }),
          catchError((error) => {
            this.patchState({ isLoading: false });
            this.snackbarService.open(`Failed to update experiment: ${error.message || error}`);
            return EMPTY;
          }),
          finalize(() => {
            if (onFinish) {
              onFinish();
            }
          }),
        );
      }),
    );
  });
}
