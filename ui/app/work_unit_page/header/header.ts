import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, computed, DestroyRef, inject, input} from '@angular/core';
import {takeUntilDestroyed} from '@angular/core/rxjs-interop';
import {MatButton} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatChipsModule} from '@angular/material/chips';
import {MatDividerModule} from '@angular/material/divider';
import {MatIcon} from '@angular/material/icon';
import {RouterModule} from '@angular/router';
import {type Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {type WorkUnit} from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import {BehaviorSubject} from 'rxjs';
import {filter, finalize, switchMap, tap} from 'rxjs/operators';

import {ConfirmDialogService} from '../../common/components/confirmation_dialog/confirmation_dialog_service';
import {PriorityChip} from '../../common/components/priority_chip/priority_chip';
import {StatusChip, StatusOption} from '../../common/components/status_chip/status_chip';
import {SnackbarService} from '../../service/snackbar_service';
import {WorkUnitsService} from '../../service/work_units_service';
import {WorkUnitPageStore} from '../work_unit_page_store';

/**
 * Work unit header component.
 */
@Component({
  selector: 'xmc-work-unit-header',
  standalone: true,
  templateUrl: './header.ng.html',
  styleUrl: './header.scss',
  imports: [
    // go/keep-sorted start
    CommonModule,
    MatButton,
    MatCardModule,
    MatChipsModule,
    MatDividerModule,
    MatIcon,
    PriorityChip,
    RouterModule,
    StatusChip,
    // go/keep-sorted end
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class Header {
  readonly workUnit = input.required<WorkUnit>();
  readonly experiment = input.required<Experiment>();

  readonly experimentId = computed(() => this.workUnit().name!.split('/')[1]);
  readonly workUnitId = computed(() => this.workUnit().name!.split('/').pop()!);
  protected isStopping$ = new BehaviorSubject<boolean>(false);
  protected isRestarting$ = new BehaviorSubject<boolean>(false);

  readonly StatusOption = StatusOption;

  private readonly workUnitsService = inject(WorkUnitsService);
  private readonly confirmDialogService = inject(ConfirmDialogService);
  private readonly snackbarService = inject(SnackbarService);
  private readonly store = inject(WorkUnitPageStore);

  private destroyRef = inject(DestroyRef);

  getStatusOption(workUnit: WorkUnit): StatusOption {
    const state: string =
        workUnit.currentWorkUnitExecution?.status?.state ?? 'STATE_UNSPECIFIED';

    switch (state) {
      case 'RUNNING':
        return StatusOption.RUNNING;
      case 'FAILED':
        return StatusOption.FAILED;
      case 'COMPLETED':
        return StatusOption.COMPLETED;
      case 'PENDING':
        return StatusOption.PENDING;
      case 'STOPPED':
        return StatusOption.NOT_RUNNING;
      default:
        return StatusOption.UNKNOWN;
    }
  }

  stop() {
    this.confirmDialogService
        .open({
          title: 'Stop Work Unit',
          message: 'Please confirm that you want to stop this work unit.',
        })
        .pipe(
            takeUntilDestroyed(this.destroyRef),
            filter(confirm => confirm),
            tap(() => {
              this.isStopping$.next(true);
            }),
            switchMap(
                () => {
                  return this.workUnitsService
                      .stopWorkUnit(
                          {xid: this.experimentId(), wid: this.workUnitId()})
                      .pipe(
                          finalize(() => {
                            this.isStopping$.next(false);
                          }),
                      );
                },
                ),
            )
        .subscribe({
          error: (error) => {
            this.snackbarService.open(
                `Failed to stop work unit, error: ${error}`);
          },
          next: () => {
            this.snackbarService.open(`Stopping work unit.`);
            this.store.fetchWorkUnitPageData();
          },
        });
  }

  restart() {
    this.confirmDialogService
        .open({
          title: 'Restart Work Unit',
          message: 'Please confirm that you want to restart this work unit.',
        })
        .pipe(
            takeUntilDestroyed(this.destroyRef),
            filter(confirm => confirm),
            tap(() => {
              this.isRestarting$.next(true);
            }),
            switchMap(
                () => {
                  return this.workUnitsService
                      .restartWorkUnit(
                          {xid: this.experimentId(), wid: this.workUnitId()})
                      .pipe(
                          finalize(() => {
                            this.isRestarting$.next(false);
                          }),
                      );
                },
                ),
            )
        .subscribe({
          error: (error) => {
            this.snackbarService.open(
                `Failed to restart work unit, error: ${error}`);
          },
          next: () => {
            this.snackbarService.open(`Restarting work unit.`);
            this.store.fetchWorkUnitPageData();
          },
        });
  }
}
