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

import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { RouterModule } from '@angular/router';
import { of } from 'rxjs';

import { ConfirmDialogService } from 'xmanager_cloud/ui/app/common/components/confirmation_dialog/confirmation_dialog_service';
import { EXPERIMENT_1, WORK_UNIT_1 } from 'xmanager_cloud/ui/app/common/mock_data';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { WorkUnitsService } from 'xmanager_cloud/ui/app/service/work_units_service';
import { bootstrap, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { WorkUnitPageStore } from '../work_unit_page_store';
import { Header } from './header';

describe('WorkUnitHeader Component', () => {
  let workUnitsServiceSpy: jasmine.SpyObj<WorkUnitsService>;
  let experimentServiceSpy: jasmine.SpyObj<ExperimentService>;
  let snackbarServiceSpy: jasmine.SpyObj<SnackbarService>;
  let confirmDialogServiceSpy: jasmine.SpyObj<ConfirmDialogService>;
  let workUnitPageStoreSpy: jasmine.SpyObj<WorkUnitPageStore>;

  beforeEach(() => {
    workUnitsServiceSpy = jasmine.createSpyObj<WorkUnitsService>('workUnitsService', [
      'stopWorkUnit',
      'restartWorkUnit',
    ]);
    experimentServiceSpy = jasmine.createSpyObj<ExperimentService>('experimentService', ['getExperiment']);
    snackbarServiceSpy = jasmine.createSpyObj<SnackbarService>('snackbarService', ['open']);
    confirmDialogServiceSpy = jasmine.createSpyObj<ConfirmDialogService>('confirmDialogService', {
      open: of(true),
    });
    workUnitPageStoreSpy = jasmine.createSpyObj<WorkUnitPageStore>('workUnitPageStore', [
      'fetchWorkUnitPageData',
    ]);

    setupModule({
      imports: [
        NoopAnimationsModule, // This makes test faster and more stable.
        RouterModule.forRoot([]),
      ],
      providers: [
        { provide: WorkUnitsService, useValue: workUnitsServiceSpy },
        { provide: ExperimentService, useValue: experimentServiceSpy },
        { provide: SnackbarService, useValue: snackbarServiceSpy },
        { provide: ConfirmDialogService, useValue: confirmDialogServiceSpy },
        { provide: WorkUnitPageStore, useValue: workUnitPageStoreSpy },
      ],
    });
  });

  it('should be created', () => {
    const comp = bootstrap(Header, {
      workUnit: WORK_UNIT_1,
      experiment: EXPERIMENT_1,
    });
    expect(comp).toBeTruthy();
  });

  it('should call stopWorkUnit and fetchWorkUnitPageData when stop is confirmed', () => {
    const comp = bootstrap(Header, {
      workUnit: WORK_UNIT_1,
      experiment: EXPERIMENT_1,
    }) as Header;

    workUnitsServiceSpy.stopWorkUnit.and.returnValue(of({}));

    comp.stop();

    expect(workUnitsServiceSpy.stopWorkUnit).toHaveBeenCalledWith({
      xid: '1',
      wid: '1',
    });
    expect(workUnitPageStoreSpy.fetchWorkUnitPageData).toHaveBeenCalled();
  });

  it('should call restartWorkUnit and fetchWorkUnitPageData when restart is confirmed', () => {
    const comp = bootstrap(Header, {
      workUnit: WORK_UNIT_1,
      experiment: EXPERIMENT_1,
    }) as Header;

    workUnitsServiceSpy.restartWorkUnit.and.returnValue(of({}));

    comp.restart();

    expect(workUnitsServiceSpy.restartWorkUnit).toHaveBeenCalledWith({
      xid: '1',
      wid: '1',
    });
    expect(workUnitPageStoreSpy.fetchWorkUnitPageData).toHaveBeenCalled();
  });
});
