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

import { TestBed } from '@angular/core/testing';
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
  beforeEach(() => {
    setupModule({
      imports: [
        NoopAnimationsModule, // This makes test faster and more stable.
        RouterModule.forRoot([]),
      ],
      providers: [
        {
          provide: WorkUnitsService,
          useValue: jasmine.createSpyObj<WorkUnitsService>('workUnitsService', [
            'stopWorkUnit',
            'restartWorkUnit',
          ]),
        },
        {
          provide: ExperimentService,
          useValue: jasmine.createSpyObj<ExperimentService>('experimentService', ['getExperiment']),
        },
        {
          provide: SnackbarService,
          useValue: jasmine.createSpyObj<SnackbarService>('snackbarService', ['open']),
        },
        {
          provide: ConfirmDialogService,
          useValue: jasmine.createSpyObj<ConfirmDialogService>('confirmDialogService', {
            open: of(true),
          }),
        },
        {
          provide: WorkUnitPageStore,
          useValue: jasmine.createSpyObj<WorkUnitPageStore>('workUnitPageStore', [
            'fetchWorkUnitPageData',
          ]),
        },
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
    const workUnitsService = TestBed.inject(WorkUnitsService) as jasmine.SpyObj<WorkUnitsService>;
    const store = TestBed.inject(WorkUnitPageStore) as jasmine.SpyObj<WorkUnitPageStore>;

    workUnitsService.stopWorkUnit.and.returnValue(of({}));

    comp.stop();

    expect(workUnitsService.stopWorkUnit).toHaveBeenCalledWith({
      xid: '1',
      wid: '1',
    });
    expect(store.fetchWorkUnitPageData).toHaveBeenCalled();
  });

  it('should call restartWorkUnit and fetchWorkUnitPageData when restart is confirmed', () => {
    const comp = bootstrap(Header, {
      workUnit: WORK_UNIT_1,
      experiment: EXPERIMENT_1,
    }) as Header;
    const workUnitsService = TestBed.inject(WorkUnitsService) as jasmine.SpyObj<WorkUnitsService>;
    const store = TestBed.inject(WorkUnitPageStore) as jasmine.SpyObj<WorkUnitPageStore>;

    workUnitsService.restartWorkUnit.and.returnValue(of({}));

    comp.restart();

    expect(workUnitsService.restartWorkUnit).toHaveBeenCalledWith({
      xid: '1',
      wid: '1',
    });
    expect(store.fetchWorkUnitPageData).toHaveBeenCalled();
  });
});
