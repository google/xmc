// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { AddExperimentDialogComponent } from './add_experiment_dialog';
import { AddExperimentDialogHarness } from './add_experiment_dialog_harness';

describe('AddExperimentDialogComponent', () => {
  let fixture: ComponentFixture<AddExperimentDialogComponent>;
  let component: AddExperimentDialogComponent;
  let harness: AddExperimentDialogHarness;
  let dialogRefSpy: jasmine.SpyObj<MatDialogRef<AddExperimentDialogComponent>>;

  beforeEach(async () => {
    dialogRefSpy = jasmine.createSpyObj('MatDialogRef', ['close']);

    await TestBed.configureTestingModule({
      imports: [AddExperimentDialogComponent, NoopAnimationsModule],
      providers: [{ provide: MatDialogRef, useValue: dialogRefSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(AddExperimentDialogComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
    harness = await TestbedHarnessEnvironment.harnessForFixture(
      fixture,
      AddExperimentDialogHarness,
    );
  });

  it('should initialize with default WID 1 and invalid form', async () => {
    expect(component.form.get('wid')?.value).toBe(1);
    expect(component.form.invalid).toBeTrue();
    expect(await harness.isSubmitButtonDisabled()).toBeTrue();
  });

  it('should be invalid when WID is null', () => {
    component.form.patchValue({
      xid: 234911123,
      wid: null,
    });
    expect(component.form.invalid).toBeTrue();
  });

  it('should close dialog with entered data on valid submit', async () => {
    component.form.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    expect(component.form.valid).toBeTrue();
    expect(await harness.isSubmitButtonDisabled()).toBeFalse();

    await harness.clickSubmit();
    expect(dialogRefSpy.close).toHaveBeenCalledWith({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
  });

  it('should close without value on cancel', async () => {
    await harness.clickCancel();
    expect(dialogRefSpy.close).toHaveBeenCalledWith();
  });

  it('should render "Add to comparison" on the submit button and submit when clicked', async () => {
    expect(await harness.getSubmitButtonText()).toBe('Add to comparison');

    component.form.patchValue({
      xid: 234911123,
      wid: 1,
    });

    expect(await harness.isSubmitButtonDisabled()).toBeFalse();
    await harness.clickSubmit();
    expect(dialogRefSpy.close).toHaveBeenCalledWith({
      xid: 234911123,
      wid: 1,
      title: undefined,
    });
  });
});
