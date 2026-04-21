import {inject, Injectable, Signal} from '@angular/core';
import {ComponentStore} from '@ngrx/component-store';
import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {WorkUnit} from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import {EMPTY} from 'rxjs';
import {switchMap, tap} from 'rxjs/operators';

import {ExperimentService} from '../service/experiment_service';
import {WorkUnitsService} from '../service/work_units_service';

/** Interface for the state of the WorkUnitPage. */
interface WorkUnitPageState {
  xid: string|null;
  wid: string|null;
  experiment: Experiment|null;
  workUnit: WorkUnit|null;
  isExperimentLoading: boolean;
  isWorkUnitLoading: boolean;
}

/** Initial state for the WorkUnitPageStore. */
const INITIAL_STATE: WorkUnitPageState = {
  xid: null,
  wid: null,
  experiment: null,
  workUnit: null,
  isExperimentLoading: false,
  isWorkUnitLoading: false,
};

/**
 * Component store for the WorkUnitPage.
 * Manages fetching and displaying work unit details.
 */
@Injectable()
export class WorkUnitPageStore extends ComponentStore<WorkUnitPageState> {
  private readonly workUnitsService = inject(WorkUnitsService);
  private readonly experimentsService = inject(ExperimentService);

  constructor() {
    super(INITIAL_STATE);
  }

  // SELECTORS
  readonly experiment: Signal<Experiment|null> = this.selectSignal(
      (state) => state.experiment,
  );

  readonly workUnit: Signal<WorkUnit|null> = this.selectSignal(
      (state) => state.workUnit,
  );

  readonly xid: Signal<string|null> = this.selectSignal(
      (state) => state.xid,
  );

  readonly wid: Signal<string|null> = this.selectSignal(
      (state) => state.wid,
  );

  readonly isLoading: Signal<boolean> = this.selectSignal(
      (state) => state.isExperimentLoading || state.isWorkUnitLoading,
  );

  // UPDATERS
  readonly setIds =
      this.updater((state, ids: {xid: string|null, wid: string|null}) => ({
                     ...state,
                     xid: ids.xid,
                     wid: ids.wid,
                   }));

  // EFFECTS
  /**
   * Fetch a work unit.
   */
  readonly getWorkUnit = this.effect<void>(
      (trigger$) => trigger$.pipe(
          tap(() => {
            this.patchState({isWorkUnitLoading: true, workUnit: null});
          }),
          switchMap(() => {
            const {xid, wid} = this.get();
            if (!xid || !wid) {
              this.patchState({isWorkUnitLoading: false});
              return EMPTY;
            }
            const request: api.GetWorkUnitRequest = {
              name: `experiments/${xid}/workUnits/${wid}`,
            };
            return this.workUnitsService.getWorkUnit(request).pipe(
                tap(
                    (workUnit: WorkUnit) => {
                      this.patchState({workUnit, isWorkUnitLoading: false});
                    },
                    (error: unknown) => {
                      console.error('Failed to get work unit:', error);
                      this.patchState({isWorkUnitLoading: false});
                    },
                    ),
            );
          }),
          ),
  );

  /**
   * Fetch an experiment.
   */
  readonly getExperiment = this.effect<void>(
      (trigger$) => trigger$.pipe(
          tap(() => {
            this.patchState({isExperimentLoading: true, experiment: null});
          }),
          switchMap(() => {
            const {xid} = this.get();
            if (!xid) {
              this.patchState({isExperimentLoading: false});
              return EMPTY;
            }
            return this.experimentsService.getExperiment(xid).pipe(
                tap(
                    (experiment: Experiment) => {
                      this.patchState({experiment, isExperimentLoading: false});
                    },
                    (error: unknown) => {
                      console.error('Failed to get experiment:', error);
                      this.patchState({isExperimentLoading: false});
                    },
                    ),
            );
          }),
          ),
  );

  fetchWorkUnitPageData(): void {
    this.getExperiment();
    this.getWorkUnit();
  }
}
