import {inject, Injectable, Signal} from '@angular/core';
import {ComponentStore} from '@ngrx/component-store';
import {UpdateExperimentRequest} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {EMPTY, Observable} from 'rxjs';
import {catchError, finalize, switchMap, tap} from 'rxjs/operators';

import {ExperimentService} from '../../../service/experiment_service';
import {SnackbarService} from '../../../service/snackbar_service';

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
export class EditExperimentFormStore extends
    ComponentStore<EditExperimentFormState> {
  private readonly experimentService = inject(ExperimentService);
  private readonly snackbarService = inject(SnackbarService);

  constructor() {
    super(INITIAL_STATE);
  }

  readonly experiment: Signal<Experiment|undefined> =
      this.selectSignal((state) => state.experiment);

  readonly isLoading: Signal<boolean> =
      this.selectSignal((state) => state.isLoading);

  readonly fetchExperiment: (xid: string) => void = this.effect(
      (xid$: Observable<string>) => {
        return xid$.pipe(
            switchMap((xid: string) => {
              this.patchState({isLoading: true});
              return this.experimentService.getExperiment(xid).pipe(
                  tap((experiment) => {
                    this.patchState({experiment, isLoading: false});
                  }),
                  catchError((error) => {
                    this.patchState({isLoading: false});
                    this.snackbarService.open(`Failed to fetch experiment: ${
                        error.message || error}`);
                    return EMPTY;
                  }),
              );
            }),
        );
      },
  );

  readonly updateExperiment = this.effect<{
    request: UpdateExperimentRequest;
    onSuccess?: () => void;
    onFinish?: () => void
  }>(
      (obs$) => {
        return obs$.pipe(
            switchMap(({request, onSuccess, onFinish}) => {
              this.patchState({isLoading: true});
              return this.experimentService.updateExperiment(request).pipe(
                  tap((experiment) => {
                    this.patchState({experiment, isLoading: false});
                    this.snackbarService.open(`Experiment updated.`);
                    if (onSuccess) {
                      onSuccess();
                    }
                  }),
                  catchError((error) => {
                    this.patchState({isLoading: false});
                    this.snackbarService.open(
                        `Failed to update experiment: ${
                            error.message || error}`,
                    );
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
      },
  );
}
