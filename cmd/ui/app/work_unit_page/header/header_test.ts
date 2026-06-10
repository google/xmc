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

import { setupModule, bootstrap } from '../../testing/test';
import {NoopAnimationsModule} from '@angular/platform-browser/animations';
import {RouterModule} from '@angular/router';
import {EXPERIMENT_1, WORK_UNIT_1} from 'xmanager_cloud/ui/app/common/mock_data';
import {of} from 'rxjs';

import {ConfirmDialogService} from '../../common/components/confirmation_dialog/confirmation_dialog_service';
import {ExperimentService} from '../../service/experiment_service';
import {SnackbarService} from '../../service/snackbar_service';
import {WorkUnitsService} from '../../service/work_units_service';
import {WorkUnitPageStore} from '../work_unit_page_store';

import {Header} from './header';

describe('WorkUnitHeader Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [
        NoopAnimationsModule,  // This makes test faster and more stable.
        RouterModule.forRoot([]),
      ],
      providers: [
        {
          provide: WorkUnitsService,
          useValue: jasmine.createSpyObj<WorkUnitsService>(
              'workUnitsService',
              ['stopWorkUnit', 'restartWorkUnit'],
              ),
        },
        {
          provide: ExperimentService,
          useValue: jasmine.createSpyObj<ExperimentService>(
              'experimentService',
              ['getExperiment'],
              ),
        },
        {
          provide: SnackbarService,
          useValue: jasmine.createSpyObj<SnackbarService>(
              'snackbarService',
              ['open'],
              ),
        },
        {
          provide: ConfirmDialogService,
          useValue: jasmine.createSpyObj<ConfirmDialogService>(
              'confirmDialogService',
              {open: of(true)},
              ),
        },
        WorkUnitPageStore,
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
});
