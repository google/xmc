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
import { MatTabGroupHarness } from '@angular/material/tabs/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { StatusMessagesListHarness } from 'xmanager_cloud/ui/app/common/components/status_messages_list/testing/status_messages_list_harness';
import { ArtifactService } from 'xmanager_cloud/ui/app/service/artifact_service';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { FeatureFlagService } from 'xmanager_cloud/ui/app/service/feature_flag_service';
import { StatusMessageService } from 'xmanager_cloud/ui/app/service/status_message_service';
import { WorkUnitsService } from 'xmanager_cloud/ui/app/service/work_units_service';

import { bootstrap, cleanState, setupModule } from '../testing/test';
import { WorkUnitPage } from './work_unit_page';

describe('WorkUnitPage Component', () => {
  const state = cleanState(() => {
    const featureFlagService = {
      isStatusMessageHistoryEnabled: true,
    };
    const workUnitsService = jasmine.createSpyObj<WorkUnitsService>('WorkUnitsService', [
      'getWorkUnit',
      'searchWorkUnits',
    ]);
    const experimentService = jasmine.createSpyObj<ExperimentService>('ExperimentService', [
      'getExperiment',
    ]);
    const artifactService = jasmine.createSpyObj<ArtifactService>('ArtifactService', [
      'listArtifacts',
    ]);
    const statusMessageService = jasmine.createSpyObj<StatusMessageService>(
      'StatusMessageService',
      ['listStatusMessages'],
    );

    workUnitsService.getWorkUnit.and.returnValue(
      of({ name: 'experiments/123/workUnits/456' }) as unknown as ReturnType<
        WorkUnitsService['getWorkUnit']
      >,
    );
    experimentService.getExperiment.and.returnValue(
      of({ name: 'experiments/123' }) as unknown as ReturnType<ExperimentService['getExperiment']>,
    );
    artifactService.listArtifacts.and.returnValue(of({ artifacts: [] }));
    statusMessageService.listStatusMessages.and.returnValue(of({ statusMessages: [] }));

    const mockActivatedRoute = {
      snapshot: {
        paramMap: convertToParamMap({ xid: '123', wid: '456' }),
      },
    };

    setupModule({
      imports: [WorkUnitPage, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        { provide: FeatureFlagService, useValue: featureFlagService },
        { provide: WorkUnitsService, useValue: workUnitsService },
        { provide: ExperimentService, useValue: experimentService },
        { provide: ArtifactService, useValue: artifactService },
        { provide: ActivatedRoute, useValue: mockActivatedRoute },
        { provide: StatusMessageService, useValue: statusMessageService },
      ],
    });

    return {
      featureFlagService,
      workUnitsService,
      experimentService,
      artifactService,
    };
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(WorkUnitPage);
    expect(comp).toBeTruthy();
  });

  it('should show Status History tab if feature flag is enabled', async () => {
    state.featureFlagService.isStatusMessageHistoryEnabled = true;
    bootstrap(WorkUnitPage);

    const tabGroup = await getHarness(MatTabGroupHarness);
    await tabGroup.selectTab({ label: 'Status History' });

    const harness = await getHarness(StatusMessagesListHarness);
    expect(harness).toBeTruthy();
  });

  it('should hide Status History tab if feature flag is disabled', async () => {
    state.featureFlagService.isStatusMessageHistoryEnabled = false;
    bootstrap(WorkUnitPage);

    const tabGroup = await getHarness(MatTabGroupHarness);
    const tabs = await tabGroup.getTabs({ label: 'Status History' });
    expect(tabs.length).toBe(0);
  });
});
