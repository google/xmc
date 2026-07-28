// Copyright 2026 Google LLC
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
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { XmcAddDashboardDialogComponent } from './add_dashboard_dialog';

const TITLE_INPUT_SELECTOR = '#dashboard-title-input';
const XIDS_INPUT_SELECTOR = '#dashboard-xids-input';
const SUBMIT_BTN_SELECTOR = '.submit-btn';

describe('XmcAddDashboardDialogComponent', () => {
  let fixture: ComponentFixture<XmcAddDashboardDialogComponent>;
  let component: XmcAddDashboardDialogComponent;
  let mockDialogRef: jasmine.SpyObj<MatDialogRef<XmcAddDashboardDialogComponent>>;

  beforeEach(async () => {
    mockDialogRef = jasmine.createSpyObj('MatDialogRef', ['close']);

    await TestBed.configureTestingModule({
      imports: [XmcAddDashboardDialogComponent, NoopAnimationsModule],
      providers: [{ provide: MatDialogRef, useValue: mockDialogRef }],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcAddDashboardDialogComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create dialog and display input form controls in DOM', () => {
    expect(component).toBeTruthy();
    const titleInput = fixture.nativeElement.querySelector(TITLE_INPUT_SELECTOR);
    expect(titleInput).toBeTruthy();
    const xidsInput = fixture.nativeElement.querySelector(XIDS_INPUT_SELECTOR);
    expect(xidsInput).toBeTruthy();
  });

  it('should validate and close dialog with parsed XIDs on submit', () => {
    component.title.set('Comparative Tuning');
    component.xidsInput.set('1001, 1002');
    const submitBtn = fixture.nativeElement.querySelector(SUBMIT_BTN_SELECTOR);
    submitBtn.click();
    expect(mockDialogRef.close).toHaveBeenCalledWith({
      title: 'Comparative Tuning',
      xids: [1001, 1002],
    });
  });
});
