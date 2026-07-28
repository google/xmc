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

import { SummaryBoxValue } from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcSummaryBoxCardComponent } from './summary_box_card';

const LATEST_VALUE_SELECTOR = '.latest-value';
const METRIC_NAME_SELECTOR = '.metric-name';
const MIN_VALUE_SELECTOR = '.min-value';
const MAX_VALUE_SELECTOR = '.max-value';

describe('XmcSummaryBoxCardComponent', () => {
  let fixture: ComponentFixture<XmcSummaryBoxCardComponent>;
  let component: XmcSummaryBoxCardComponent;

  const mockSummary: SummaryBoxValue = {
    metricName: 'loss/train',
    latestValue: 0.1234,
    minValue: 0.1,
    maxValue: 2.5,
    meanValue: 0.45,
    latestIteration: 1000,
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [XmcSummaryBoxCardComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcSummaryBoxCardComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('summary', mockSummary);
    fixture.detectChanges();
  });

  it('should create summary card and display latest value in DOM', () => {
    expect(component).toBeTruthy();
    const latestValEl = fixture.nativeElement.querySelector(LATEST_VALUE_SELECTOR);
    expect(latestValEl).toBeTruthy();
    expect(latestValEl.textContent).toContain('0.1234');
  });

  it('should display metric name and min/max statistics', () => {
    const metricNameEl = fixture.nativeElement.querySelector(METRIC_NAME_SELECTOR);
    expect(metricNameEl.textContent).toContain('loss/train');
    const minValEl = fixture.nativeElement.querySelector(MIN_VALUE_SELECTOR);
    expect(minValEl.textContent).toContain('0.1');
    const maxValEl = fixture.nativeElement.querySelector(MAX_VALUE_SELECTOR);
    expect(maxValEl.textContent).toContain('2.5');
  });
});
