import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, DestroyRef, inject, ViewChild} from '@angular/core';
import {takeUntilDestroyed} from '@angular/core/rxjs-interop';
import {MatButtonModule} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatChipsModule} from '@angular/material/chips';
import {MatDividerModule} from '@angular/material/divider';
import {MatIconModule} from '@angular/material/icon';
import {MatTabsModule} from '@angular/material/tabs';
import {MatTooltipModule} from '@angular/material/tooltip';
import {ActivatedRoute} from '@angular/router';
import {Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {Tag} from 'xmanager_cloud/ui/app/common/components/tag/tag';
import {ProcessedUrl, processUrls} from 'xmanager_cloud/ui/app/common/url/url_processor';
import {ExperimentService} from 'xmanager_cloud/ui/app/service/experiment_service';
import {FeatureFlagService} from 'xmanager_cloud/ui/app/service/feature_flag_service';
import {SnackbarService} from 'xmanager_cloud/ui/app/service/snackbar_service';
import {WorkUnitsService} from 'xmanager_cloud/ui/app/service/work_units_service';
import {BehaviorSubject, combineLatest, Observable} from 'rxjs';
import {filter, finalize, map, shareReplay, switchMap, tap, withLatestFrom} from 'rxjs/operators';

import {ArtifactsList} from '../common/components/artifacts_list/artifacts_list';
import {AutoReloadService, ReloaderControl} from '../common/components/autoreloader/autoreloader_service';
import {ConfirmDialogService} from '../common/components/confirmation_dialog/confirmation_dialog_service';
import {LoadingBar} from '../common/components/loading_bar/loading_bar';
import {PriorityChip} from '../common/components/priority_chip/priority_chip';
import {StatusChip, StatusOption} from '../common/components/status_chip/status_chip';
import {StatusMessage} from '../common/components/status_message/status_message';
import {StatusMessagesList} from '../common/components/status_messages_list/status_messages_list';
import {UrlsWidget} from '../common/components/urls_widget/urls_widget';
import {WorkUnitsStatusBar} from '../common/components/work_units_status_bar/work_units_status_bar';
import {XidBadge} from '../common/components/xid_badge/xid_badge';
import {EditExperimentFormData} from '../side_panel/contents/edit_experiment_form/edit_experiment_form';
import {SidePanelService} from '../side_panel/side_panel_service';

import {WorkUnitsTable} from './work_units_table/work_units_table';

/**
 * Display details for a single experiment.
 */
@Component({
  selector: 'xmc-experiment-page',
  standalone: true,
  imports: [
    CommonModule,     MatCardModule,  MatButtonModule,    MatTabsModule,
    MatIconModule,    MatChipsModule, MatDividerModule,   LoadingBar,
    WorkUnitsTable,   PriorityChip,   ArtifactsList,      Tag,
    XidBadge,         StatusChip,     StatusMessage,      WorkUnitsStatusBar,
    MatTooltipModule, UrlsWidget,     StatusMessagesList,
  ],
  templateUrl: './experiment_page.ng.html',
  styleUrl: './experiment_page.css',
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class ExperimentPage {
  @ViewChild(WorkUnitsTable) workUnitsTable?: WorkUnitsTable;
  protected xid$: Observable<string>;
  protected experiment$: Observable<Experiment>;
  protected isExperimentLoading$ = new BehaviorSubject<boolean>(false);
  protected urls$: Observable<ProcessedUrl[]>;
  protected readonly StatusOption = StatusOption;
  private readonly refreshTrigger = new BehaviorSubject<void>(undefined);
  protected isEditing$ = new BehaviorSubject<boolean>(false);
  protected isStopping$ = new BehaviorSubject<boolean>(false);
  protected isRestarting$ = new BehaviorSubject<boolean>(false);

  private readonly experimentService = inject(ExperimentService);
  private readonly workUnitsService = inject(WorkUnitsService);
  private readonly snackbarService = inject(SnackbarService);
  private readonly confirmDialogService = inject(ConfirmDialogService);
  private readonly sidePanelService = inject(SidePanelService);
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly featureFlagService = inject(FeatureFlagService);
  private readonly reloader: ReloaderControl =
      this.autoReloadService.createReloader({emitOnStart: false});
  private destroyRef = inject(DestroyRef);

  get isStatusMessageHistoryEnabled(): boolean {
    return this.featureFlagService.isStatusMessageHistoryEnabled;
  }

  constructor() {
    const route = inject(ActivatedRoute);

    this.xid$ = route.paramMap.pipe(map(params => params.get('xid')!));
    this.experiment$ =
        combineLatest([
          this.xid$,
          this.refreshTrigger,
        ])
            .pipe(
                tap(([xid, _]) => {
                  this.isExperimentLoading$.next(true);
                }),
                switchMap(
                    ([xid, _]) =>
                        this.experimentService.getExperiment(xid).pipe(
                            finalize(() => {
                              this.isExperimentLoading$.next(false);
                            }),
                            tap({
                              error: (error) => {
                                this.snackbarService.open(
                                    `Failed to fetch experiment: ${
                                        error.message || error}`);
                              }
                            }))),
                shareReplay(1),
            );

    this.urls$ = this.experiment$.pipe(
        map(experiment => processUrls(experiment?.urls ?? [], experiment)),
    );

    this.reloader.reloader$.pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe(() => {
          this.refresh();
        });
    this.reloader.start();
    this.destroyRef.onDestroy(() => {
      this.reloader.stop();
    });
  }

  refresh() {
    this.refreshTrigger.next();
    this.workUnitsTable?.refresh();
  }

  stopExperiment() {
    this.confirmDialogService
        .open({
          title: 'Stop Experiment',
          message:
              'Are you sure you want to stop all of the work units in this experiment?',
        })
        .pipe(
            takeUntilDestroyed(this.destroyRef),
            filter(confirm => confirm),
            tap(() => {
              this.isStopping$.next(true);
            }),
            withLatestFrom(this.xid$),
            switchMap(
                ([confirm, xid]) => {
                  return this.workUnitsService.batchStopWorkUnits(xid).pipe(
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
                `Failed to stop experiment, error: ${error}`);
          },
          next: () => {
            this.snackbarService.open(`Stopping experiment.`);
            this.refresh();
          },
        });
  }

  restartExperiment() {
    this.confirmDialogService
        .open({
          title: 'Restart Experiment',
          message:
              'Are you sure you want to restart all of the work units in this experiment?',
        })
        .pipe(
            takeUntilDestroyed(this.destroyRef),
            filter(confirm => confirm),
            tap(() => {
              this.isRestarting$.next(true);
            }),
            withLatestFrom(this.xid$),
            switchMap(
                ([confirm, xid]) => {
                  return this.workUnitsService.batchRestartWorkUnits(xid).pipe(
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
                `Failed to restart experiment, error: ${error}`);
          },
          next: () => {
            this.snackbarService.open(`Restarting experiment.`);
            this.refresh();
          },
        });
  }

  editExperiment(xid: string) {
    this.sidePanelService.openSidePanel(
        new EditExperimentFormData(
            xid,
            /* onUpdateSuccess= */
            () => {
              this.refresh();
            },
            /* onUpdateStart= */
            () => {
              this.isEditing$.next(true);
            },
            /* onUpdateFinish= */
            () => {
              this.isEditing$.next(false);
            },
            ),
    );
  }

  getStatusChip(experiment: Experiment): StatusOption {
    if (experiment?.status?.activeState === 'ACTIVE') {
      return StatusOption.RUNNING;
    } else {
      return StatusOption.NOT_RUNNING;
    }
  }

  isExperimentStoppable(experiment: Experiment): boolean {
    return experiment?.status?.activeState === 'ACTIVE';
  }
}
