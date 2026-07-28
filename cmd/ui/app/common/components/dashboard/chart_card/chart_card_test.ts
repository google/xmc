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
import { of } from 'rxjs';

import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { Chart, LayoutType, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcChartCardComponent } from './chart_card';

const REFRESH_BTN_SELECTOR = '.refresh-btn';
const TITLE_SELECTOR = '.chart-title';

describe('XmcChartCardComponent', () => {
  let fixture: ComponentFixture<XmcChartCardComponent>;
  let component: XmcChartCardComponent;
  let mockDashboardService: jasmine.SpyObj<DashboardService>;

  const mockChart: Chart = {
    name: 'experiments/1/charts/c-1',
    title: 'Loss Evaluation',
    xid: 1,
    plots: [
      {
        plotId: 'p-1',
        title: 'Loss curve',
        type: PlotType.SCATTER,
        data: { metrics: ['loss/train'], sources: [{ xid: 1 }] },
      },
    ],
    plotLayout: { type: LayoutType.STACKED },
  };

  beforeEach(async () => {
    mockDashboardService = jasmine.createSpyObj('DashboardService', ['getChartData']);
    mockDashboardService.getChartData.and.returnValue(
      of({
        name: mockChart.name,
        xid: 1,
        plotsData: [],
      }),
    );

    await TestBed.configureTestingModule({
      imports: [XmcChartCardComponent],
      providers: [{ provide: DashboardService, useValue: mockDashboardService }],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcChartCardComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('chart', mockChart);
    fixture.detectChanges();
  });

  it('should create chart card and display title in DOM', () => {
    expect(component).toBeTruthy();
    const titleEl = fixture.nativeElement.querySelector(TITLE_SELECTOR);
    expect(titleEl.textContent).toContain('Loss Evaluation');
  });

  it('should call getChartData on init and when refresh button is clicked', () => {
    expect(mockDashboardService.getChartData).toHaveBeenCalledWith({
      name: mockChart.name,
    });
    const refreshBtn = fixture.nativeElement.querySelector(REFRESH_BTN_SELECTOR);
    refreshBtn.click();
    fixture.detectChanges();
    expect(mockDashboardService.getChartData).toHaveBeenCalledTimes(2);
  });
});
