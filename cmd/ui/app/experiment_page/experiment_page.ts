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
import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, DestroyRef, inject, viewChild } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatTabsModule } from '@angular/material/tabs';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, RouterModule } from '@angular/router';
import { BehaviorSubject, combineLatest, Observable } from 'rxjs';
import { filter, finalize, map, shareReplay, switchMap, tap, withLatestFrom } from 'rxjs/operators';

import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { ArtifactsList } from 'xmanager_cloud/ui/app/common/components/artifacts_list/artifacts_list';
import {
  AutoReloadService,
  ReloaderControl,
} from 'xmanager_cloud/ui/app/common/components/autoreloader/autoreloader_service';
import { ConfirmDialogService } from 'xmanager_cloud/ui/app/common/components/confirmation_dialog/confirmation_dialog_service';
import { LoadingBar } from 'xmanager_cloud/ui/app/common/components/loading_bar/loading_bar';
import { PriorityChip } from 'xmanager_cloud/ui/app/common/components/priority_chip/priority_chip';
import {
  StatusChip,
  StatusOption,
} from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';
import { StatusMessage } from 'xmanager_cloud/ui/app/common/components/status_message/status_message';
import { StatusMessagesList } from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list';
import { Tag } from 'xmanager_cloud/ui/app/common/components/tag/tag';
import { UrlsWidget } from 'xmanager_cloud/ui/app/common/components/urls_widget/urls_widget';
import { WorkUnitsStatusBar } from 'xmanager_cloud/ui/app/common/components/work_units_status_bar/work_units_status_bar';
import { XidBadge } from 'xmanager_cloud/ui/app/common/components/xid_badge/xid_badge';
import { ProcessedUrl, processUrls } from 'xmanager_cloud/ui/app/common/url/url_processor';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { FeatureFlagService } from 'xmanager_cloud/ui/app/service/feature_flag_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { WorkUnitsService } from 'xmanager_cloud/ui/app/service/work_units_service';
import { EditExperimentFormData } from 'xmanager_cloud/ui/app/side_panel/contents/edit_experiment_form/edit_experiment_form';
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';

import { WorkUnitsTable } from './work_units_table/work_units_table';

/**
 * Display details for a single experiment.
 */
@Component({
  selector: 'xmc-experiment-page',
  standalone: true,
  imports: [
    CommonModule,
    RouterModule,
    MatCardModule,
    MatButtonModule,
    MatTabsModule,
    MatIconModule,
    MatChipsModule,
    MatDividerModule,
    LoadingBar,
    WorkUnitsTable,
    PriorityChip,
    ArtifactsList,
    Tag,
    XidBadge,
    StatusChip,
    StatusMessage,
    WorkUnitsStatusBar,
    MatTooltipModule,
    UrlsWidget,
    StatusMessagesList,
  ],
  templateUrl: './experiment_page.ng.html',
  styleUrl: './experiment_page.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ExperimentPage {
  readonly workUnitsTable = viewChild(WorkUnitsTable);
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
  private readonly reloader: ReloaderControl = this.autoReloadService.createReloader({
    emitOnStart: false,
  });
  private readonly destroyRef = inject(DestroyRef);

  get isStatusMessageHistoryEnabled(): boolean {
    return this.featureFlagService.isStatusMessageHistoryEnabled;
  }

  constructor() {
    const route = inject(ActivatedRoute);

    this.xid$ = route.paramMap.pipe(map((params) => params.get('xid')!));
    this.experiment$ = this.initExperimentStream();
    this.urls$ = this.initUrlsStream();

    this.reloader.reloader$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.refresh();
    });
    this.reloader.start();
    this.destroyRef.onDestroy(() => {
      this.reloader.stop();
    });
  }

  refresh() {
    this.refreshTrigger.next();
    this.workUnitsTable()?.refresh();
  }

  stopExperiment() {
    this.executeBatchOperation(
      /* operationName= */ 'stop experiment',
      /* confirmTitle= */ 'Stop Experiment',
      /* confirmMessage= */ 'Are you sure you want to stop all of the work units in this experiment?',
      /* loadingSubject$= */ this.isStopping$,
      /* batchAction= */ (xid) => this.workUnitsService.batchStopWorkUnits(xid),
      /* successMessage= */ 'Stopping experiment.',
    );
  }

  restartExperiment() {
    this.executeBatchOperation(
      /* operationName= */ 'restart experiment',
      /* confirmTitle= */ 'Restart Experiment',
      /* confirmMessage= */ 'Are you sure you want to restart all of the work units in this experiment?',
      /* loadingSubject$= */ this.isRestarting$,
      /* batchAction= */ (xid) => this.workUnitsService.batchRestartWorkUnits(xid),
      /* successMessage= */ 'Restarting experiment.',
    );
  }

  editExperiment(xid: string) {
    this.sidePanelService.openSidePanel(
      new EditExperimentFormData(
        xid,
        /* onUpdateSuccess= */ () => {
          this.refresh();
        },
        /* onUpdateStart= */ () => {
          this.isEditing$.next(true);
        },
        /* onUpdateFinish= */ () => {
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

  private initExperimentStream(): Observable<Experiment> {
    return combineLatest([this.xid$, this.refreshTrigger]).pipe(
      tap(() => {
        this.isExperimentLoading$.next(true);
      }),
      switchMap(([xid]) =>
        this.experimentService.getExperiment(xid).pipe(
          finalize(() => {
            this.isExperimentLoading$.next(false);
          }),
          tap({
            error: (error) => {
              this.snackbarService.open(`Failed to fetch experiment: ${error.message || error}`);
            },
          }),
        ),
      ),
      shareReplay(1),
    );
  }

  private initUrlsStream(): Observable<ProcessedUrl[]> {
    return this.experiment$.pipe(
      map((experiment) => processUrls(experiment?.urls ?? [], experiment)),
    );
  }

  private executeBatchOperation(
    operationName: string,
    confirmTitle: string,
    confirmMessage: string,
    loadingSubject$: BehaviorSubject<boolean>,
    batchAction: (xid: string) => Observable<unknown>,
    successMessage: string,
  ) {
    this.confirmDialogService
      .open({
        title: confirmTitle,
        message: confirmMessage,
      })
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        filter((confirm) => confirm),
        tap(() => {
          loadingSubject$.next(true);
        }),
        withLatestFrom(this.xid$),
        switchMap(([_, xid]) => {
          return batchAction(xid).pipe(
            finalize(() => {
              loadingSubject$.next(false);
            }),
          );
        }),
      )
      .subscribe(this.getBatchObserver(operationName, successMessage));
  }

  private getBatchObserver(operationName: string, successMessage: string) {
    return {
      error: (error: unknown) => {
        this.snackbarService.open(`Failed to ${operationName}, error: ${error}`);
      },
      next: () => {
        this.snackbarService.open(successMessage);
        this.refresh();
      },
    };
  }
}
