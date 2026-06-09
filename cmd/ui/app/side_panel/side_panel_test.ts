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
import { cleanState, setupModule, bootstrap, get, getDebugEl, hasEl, flush, setupObservableMatchers } from '../testing/test';
import 'jasmine';

import {getHarness} from '@angular/cdk/testing/catalyst/async';
import {MatSidenavHarness} from '@angular/material/sidenav/testing';
import {NoopAnimationsModule} from '@angular/platform-browser/animations';
import {of} from 'rxjs';

import {EXPERIMENT_1} from '../common/mock_data';
import {ExperimentService} from '../service/experiment_service';

import {EditExperimentFormData} from './contents/edit_experiment_form/edit_experiment_form';
import {SidePanel} from './side_panel';
import {SidePanelService} from './side_panel_service';

describe('SidePanel Component', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(async () => {
    const mockExperimentService = jasmine.createSpyObj<ExperimentService>(
        'ExperimentService',
        ['getExperiment'],
    );
    mockExperimentService.getExperiment.and.returnValue(
        of(EXPERIMENT_1),
    );

    setupModule({
      imports: [
        SidePanel,
        NoopAnimationsModule,
      ],
      providers:
          [{provide: ExperimentService, useValue: mockExperimentService}],
    });

    const component = bootstrap(SidePanel);
    const service = getDebugEl().injector.get(SidePanelService);

    return {
      component,
      service,
      mockExperimentService,
    };
  }, beforeEach);

  it('should create the component', () => {
    expect(state.component).toBeTruthy();
  });

  it('should have the sidenav closed by default', async () => {
    const sidenavHarness = await getHarness(MatSidenavHarness);
    expect(await sidenavHarness.isOpen()).toBeFalse();
  });

  it('should open the sidenav and load the edit experiment form when EditExperimentFormData is added to the store',
     async () => {
       const requestA = new EditExperimentFormData('12345');
       state.service.openSidePanel(requestA);
       await flush();

       const sidenavHarness = await getHarness(MatSidenavHarness);
       expect(await sidenavHarness.isOpen()).toBeTrue();
       expect(hasEl('xmc-edit-experiment-form')).toBeTrue();
     });

  it('should close the sidenav when closeSidePanel is called', async () => {
    const requestA = new EditExperimentFormData('12345');
    state.service.openSidePanel(requestA);
    await flush();

    state.service.closeSidePanel();
    await flush();

    const sidenavHarness = await getHarness(MatSidenavHarness);
    expect(await sidenavHarness.isOpen()).toBeFalse();
  });
});
