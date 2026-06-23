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

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, convertToParamMap, ParamMap, provideRouter } from '@angular/router';
import { BehaviorSubject, NEVER, of } from 'rxjs';

import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { AutoReloadService } from 'xmanager_cloud/ui/app/common/components/autoreloader/autoreloader_service';
import { ConfirmDialogService } from 'xmanager_cloud/ui/app/common/components/confirmation_dialog/confirmation_dialog_service';
import { ArtifactService } from 'xmanager_cloud/ui/app/service/artifact_service';
import {
  AUTO_REFRESH_INTERVAL_MS,
  DEFAULT_REFRESH_INTERVAL_MS,
} from 'xmanager_cloud/ui/app/service/constants';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { FeatureFlagService } from 'xmanager_cloud/ui/app/service/feature_flag_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { StatusMessageService } from 'xmanager_cloud/ui/app/service/status_message_service';
import { WorkUnitsService } from 'xmanager_cloud/ui/app/service/work_units_service';
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';
import { bootstrap, cleanState, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { ExperimentPage } from './experiment_page';
import { ExperimentPageHarness } from './testing/experiment_page_harness';

const MOCK_EXPERIMENT: Experiment = {
  name: 'experiments/123',
  title: 'Test Experiment',
  creator: 'test-user',
  createTime: '2023-10-27T10:00:00Z',
  status: {
    activeState: 'ACTIVE',
    childStateCounts: [
      { state: 'RUNNING', count: '1' },
      { state: 'COMPLETED', count: '1' },
    ],
  },
};

describe('ExperimentPage Component', () => {
  const state = cleanState(() => {
    const experimentService = jasmine.createSpyObj<ExperimentService>('ExperimentService', [
      'getExperiment',
    ]);
    const workUnitsService = jasmine.createSpyObj<WorkUnitsService>('WorkUnitsService', [
      'batchStopWorkUnits',
      'batchRestartWorkUnits',
      'searchWorkUnits',
    ]);
    const snackbarService = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);
    const confirmDialogService = jasmine.createSpyObj<ConfirmDialogService>(
      'ConfirmDialogService',
      ['open'],
    );
    const sidePanelService = jasmine.createSpyObj<SidePanelService>('SidePanelService', [
      'openSidePanel',
    ]);
    const artifactService = jasmine.createSpyObj<ArtifactService>('ArtifactService', [
      'listArtifacts',
    ]);
    const statusMessageService = jasmine.createSpyObj<StatusMessageService>(
      'StatusMessageService',
      ['listStatusMessages'],
    );
    const featureFlagService = {
      isStatusMessageHistoryEnabled: true,
    };
    const autoReloadServiceMock = {
      createReloader: () => ({
        reloader$: NEVER,
        start: () => {},
        stop: () => {},
      }),
    };

    experimentService.getExperiment.and.returnValue(of(MOCK_EXPERIMENT));
    workUnitsService.searchWorkUnits.and.returnValue(of({ workUnits: [] }));
    artifactService.listArtifacts.and.returnValue(of({ artifacts: [] }));
    statusMessageService.listStatusMessages.and.returnValue(of({ statusMessages: [] }));

    const paramMap = new BehaviorSubject<ParamMap>(convertToParamMap({ xid: '123' }));
    const mockActivatedRoute = {
      paramMap: paramMap.asObservable(),
    };

    setupModule({
      imports: [
        ExperimentPage,
        NoopAnimationsModule, // This makes test faster and more stable.
      ],
      providers: [
        provideRouter([]),
        {
          provide: AUTO_REFRESH_INTERVAL_MS,
          useValue: DEFAULT_REFRESH_INTERVAL_MS,
        },
        { provide: ExperimentService, useValue: experimentService },
        { provide: WorkUnitsService, useValue: workUnitsService },
        { provide: SnackbarService, useValue: snackbarService },
        { provide: ConfirmDialogService, useValue: confirmDialogService },
        { provide: SidePanelService, useValue: sidePanelService },
        { provide: ActivatedRoute, useValue: mockActivatedRoute },
        { provide: ArtifactService, useValue: artifactService },
        { provide: FeatureFlagService, useValue: featureFlagService },
        { provide: StatusMessageService, useValue: statusMessageService },
        { provide: AutoReloadService, useValue: autoReloadServiceMock },
      ],
    });

    return {
      experimentService,
      workUnitsService,
      snackbarService,
      confirmDialogService,
      sidePanelService,
      mockActivatedRoute,
      paramMap,
      artifactService,
      featureFlagService,
    };
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(ExperimentPage);

    expect(comp).toBeTruthy();
    expect(state.experimentService.getExperiment).toHaveBeenCalledWith('123');
  });

  it('should stop experiment when confirmed', async () => {
    state.confirmDialogService.open.and.returnValue(of(true));
    state.workUnitsService.batchStopWorkUnits.and.returnValue(of({}));
    bootstrap(ExperimentPage);

    const harness = await getHarness(ExperimentPageHarness);
    await harness.clickStopExperiment();

    expect(state.confirmDialogService.open).toHaveBeenCalled();
    expect(state.workUnitsService.batchStopWorkUnits).toHaveBeenCalledWith('123');
    expect(state.snackbarService.open).toHaveBeenCalledWith('Stopping experiment.');
  });

  it('should restart experiment when confirmed', async () => {
    state.confirmDialogService.open.and.returnValue(of(true));
    state.workUnitsService.batchRestartWorkUnits.and.returnValue(of({}));
    bootstrap(ExperimentPage);

    const harness = await getHarness(ExperimentPageHarness);
    await harness.clickRestartExperiment();

    expect(state.confirmDialogService.open).toHaveBeenCalled();
    expect(state.workUnitsService.batchRestartWorkUnits).toHaveBeenCalledWith('123');
    expect(state.snackbarService.open).toHaveBeenCalledWith('Restarting experiment.');
  });

  it('should open side panel for editing experiment', async () => {
    bootstrap(ExperimentPage);

    const harness = await getHarness(ExperimentPageHarness);
    await harness.clickEditExperiment();

    expect(state.sidePanelService.openSidePanel).toHaveBeenCalled();
  });

  describe('Loading states', () => {
    it('should show loading bar', async () => {
      state.experimentService.getExperiment.and.returnValue(NEVER);
      bootstrap(ExperimentPage);
      const harness = await getHarness(ExperimentPageHarness);

      expect(await harness.isLoading()).toBeTrue();
      expect(await harness.hasTitle()).toBeFalse();
    });

    it('should show refreshing', async () => {
      state.experimentService.getExperiment.and.returnValues(of(MOCK_EXPERIMENT), NEVER);
      const comp = bootstrap(ExperimentPage);
      comp.refresh();
      const harness = await getHarness(ExperimentPageHarness);

      expect(await harness.isRefreshing()).toBeTrue();
      expect(await harness.hasTitle()).toBeTrue();
    });

    it('should hide loading bar when not fetching', async () => {
      state.experimentService.getExperiment.and.returnValue(of(MOCK_EXPERIMENT));
      bootstrap(ExperimentPage);
      const harness = await getHarness(ExperimentPageHarness);

      expect(await harness.hasLoadingBar()).toBeFalse();
      expect(await harness.hasTitle()).toBeTrue();
    });
  });

  describe('Feature flags', () => {
    it('should show Status History tab if feature flag is enabled', async () => {
      state.featureFlagService.isStatusMessageHistoryEnabled = true;

      bootstrap(ExperimentPage);
      const harness = await getHarness(ExperimentPageHarness);

      expect(await harness.hasStatusHistoryTab()).toBeTrue();
    });

    it('should hide Status History tab if feature flag is disabled', async () => {
      state.featureFlagService.isStatusMessageHistoryEnabled = false;

      bootstrap(ExperimentPage);
      const harness = await getHarness(ExperimentPageHarness);

      expect(await harness.hasStatusHistoryTab()).toBeFalse();
    });
  });
});
