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

import 'jasmine';

import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { AddExperimentDialogComponent } from './add_experiment_dialog';

describe('AddExperimentDialogComponent', () => {
  let fixture: ComponentFixture<AddExperimentDialogComponent>;
  let component: AddExperimentDialogComponent;
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
  });

  it('should initialize with invalid form', () => {
    expect(component.form.invalid).toBeTrue();
  });

  it('should close dialog with entered data on valid submit', () => {
    component.form.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    expect(component.form.valid).toBeTrue();

    component.onSubmit();
    expect(dialogRefSpy.close).toHaveBeenCalledWith({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
  });

  it('should close without value on cancel', () => {
    component.onCancel();
    expect(dialogRefSpy.close).toHaveBeenCalledWith();
  });
});
