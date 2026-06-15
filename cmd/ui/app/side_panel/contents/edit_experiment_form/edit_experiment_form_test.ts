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
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatButtonToggleHarness } from '@angular/material/button-toggle/testing';
import { MatInputHarness } from '@angular/material/input/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of } from 'rxjs';

import { EXPERIMENT_1 } from 'xmanager_cloud/ui/app/common/mock_data';
import { ExperimentService } from 'xmanager_cloud/ui/app/service/experiment_service';
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';
import {
  bootstrap,
  cleanState,
  get,
  getEl,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { EditExperimentForm, EditExperimentFormData } from './edit_experiment_form';

describe('EditExperimentForm Component', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(() => {
    const mockExperimentService = jasmine.createSpyObj<ExperimentService>('ExperimentService', [
      'getExperiment',
      'updateExperiment',
    ]);

    setupModule({
      imports: [EditExperimentForm, NoopAnimationsModule],
      providers: [{ provide: ExperimentService, useValue: mockExperimentService }],
    });

    return {
      mockExperimentService,
      sidePanelService: get(SidePanelService),
    };
  }, beforeEach);

  it('should be created with XID shown and initial fields set', async () => {
    state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));

    const component = bootstrap(EditExperimentForm, { data: new EditExperimentFormData('12345') });

    // Assert that the component is created with correct header
    expect(component).toBeTruthy();
    expect(getEl('.header').textContent).toContain('Edit Experiment');
    expect(getEl('.header').textContent).toContain('12345');

    // Assert that the forms are filled in correctly
    const nameInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="name"]' }),
    );
    expect(await nameInput.getValue()).toBe(EXPERIMENT_1.title!);

    const notesInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="notes"]' }),
    );
    expect(await notesInput.getValue()).toBe(EXPERIMENT_1.notes!);

    const tagsInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="tags"]' }),
    );
    expect(await tagsInput.getValue()).toBe(EXPERIMENT_1.tags!.join(','));

    const priorityInput = await getHarness(
      MatButtonToggleHarness.with({ text: 'Low', checked: true }),
    );
    expect(priorityInput).toBeTruthy();
  });

  it('should call the ExperimentService#updateExperiment correctly and close the side panel when the user edits the fields and clicks on the save button', async () => {
    state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));
    state.mockExperimentService.updateExperiment.and.returnValue(of(EXPERIMENT_1));

    bootstrap(EditExperimentForm, { data: new EditExperimentFormData('12345') });

    // Update the name, notes, tags, and priority fields
    const nameInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="name"]' }),
    );
    await nameInput.setValue('Updated name');

    const notesInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="notes"]' }),
    );
    await notesInput.setValue('Updated notes');

    const tagsInput = await getHarness(
      MatInputHarness.with({ selector: '[formControlName="tags"]' }),
    );
    await tagsInput.setValue('  one tag, another tag  ,  ,');

    const priorityInput = await getHarness(MatButtonToggleHarness.with({ text: 'High' }));
    await priorityInput.check();

    const saveButton = await getHarness(
      MatButtonHarness.with({ selector: '[data-testid="save-button"]' }),
    );
    await saveButton.click();

    // Assert that the ExperimentService#updateExperiment is called with the
    // correct request
    expect(state.mockExperimentService.updateExperiment).toHaveBeenCalledWith({
      experiment: {
        ...EXPERIMENT_1,
        title: 'Updated name',
        notes: 'Updated notes',
        tags: ['one tag', 'another tag'],
        priority: 'HIGH',
      },
      updateMask: 'title,notes,tags,priority',
    });

    // Assert that the side panel is closed
    await expectAsync(state.sidePanelService.dialogRequest$).toEmit(undefined);
  });

  it('should close the side panel when the user clicks on the cancel button', async () => {
    state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));

    bootstrap(EditExperimentForm, { data: new EditExperimentFormData('12345') });

    const cancelButton = await getHarness(
      MatButtonHarness.with({ selector: '[data-testid="cancel-button"]' }),
    );
    await cancelButton.click();

    // Assert that the ExperimentService#updateExperiment is not called
    expect(state.mockExperimentService.updateExperiment).not.toHaveBeenCalled();

    // Assert that the side panel is closed
    await expectAsync(state.sidePanelService.dialogRequest$).toEmit(undefined);
  });

  describe('name validator', () => {
    it('should be invalid if name is empty', async () => {
      state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));

      const component = bootstrap(EditExperimentForm, {
        data: new EditExperimentFormData('12345'),
      });
      const nameInput = await getHarness(
        MatInputHarness.with({ selector: '[formControlName="name"]' }),
      );
      await nameInput.setValue('');

      expect(component.editExperimentFormGroup().controls.name.errors).toEqual({
        required: true,
        whitespace: true,
      });
    });

    it('should be invalid if name consists of only whitespace', async () => {
      state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));

      const component = bootstrap(EditExperimentForm, {
        data: new EditExperimentFormData('12345'),
      });
      const nameInput = await getHarness(
        MatInputHarness.with({ selector: '[formControlName="name"]' }),
      );
      await nameInput.setValue('   ');

      expect(component.editExperimentFormGroup().controls.name.errors).toEqual({
        whitespace: true,
      });
    });

    it('should be valid if name is not empty or whitespace', async () => {
      state.mockExperimentService.getExperiment.and.returnValue(of(EXPERIMENT_1));

      const component = bootstrap(EditExperimentForm, {
        data: new EditExperimentFormData('12345'),
      });
      const nameInput = await getHarness(
        MatInputHarness.with({ selector: '[formControlName="name"]' }),
      );
      await nameInput.setValue('not only whitespace');

      expect(component.editExperimentFormGroup().controls.name.errors).toBeNull();
    });
  });
});
