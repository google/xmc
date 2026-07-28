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

import { XmcEchartsWrapperComponent } from './echarts_wrapper';

const CONTAINER_SELECTOR = '.chart-container';

describe('XmcEchartsWrapperComponent', () => {
  let fixture: ComponentFixture<XmcEchartsWrapperComponent>;
  let component: XmcEchartsWrapperComponent;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [XmcEchartsWrapperComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcEchartsWrapperComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('options', {
      xAxis: { type: 'category', data: ['A', 'B'] },
      yAxis: { type: 'value' },
      series: [{ type: 'bar', data: [10, 20] }],
    });
    fixture.detectChanges();
  });

  it('should create and render chart container element in DOM', () => {
    expect(component).toBeTruthy();
    const container = fixture.nativeElement.querySelector(CONTAINER_SELECTOR);
    expect(container).toBeTruthy();
    expect(component.getChartInstance()).toBeDefined();
  });

  it('should update chart options reactively when input signal changes', () => {
    fixture.componentRef.setInput('options', {
      xAxis: { type: 'category', data: ['X', 'Y'] },
      yAxis: { type: 'value' },
      series: [{ type: 'line', data: [5, 15] }],
    });
    fixture.detectChanges();
    expect(component.getChartInstance()?.getOption()).toBeDefined();
  });
});
