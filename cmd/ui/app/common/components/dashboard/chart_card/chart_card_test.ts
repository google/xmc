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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import {
  Chart,
  ChartData,
  LayoutType,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { ChartCardComponent } from './chart_card';
import { ChartCardHarness } from './chart_card_harness';

@Component({
  standalone: true,
  imports: [ChartCardComponent],
  template: ` <xmc-chart-card [chart]="chart()" /> `,
})
class TestHostComponent {
  readonly chart = signal<Chart>({
    name: 'experiments/9001/charts/mixed_overview',
    title: 'VCC Baseline',
    xid: 234911122,
    wid: 1,
    plotLayout: { type: LayoutType.GRID },
    plots: [
      {
        plotId: 'loss_kpi',
        title: 'Last validation loss',
        type: PlotType.SUMMARY_BOX,
        data: { metrics: ['train/loss'] },
      },
      {
        plotId: 'loss_curve',
        title: 'Validation loss vs steps',
        type: PlotType.SCATTER,
        data: { metrics: ['eval/loss'] },
      },
    ],
  });
}

describe('ChartCardComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: ChartCardHarness;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  const mockChartData: ChartData = {
    name: 'experiments/9001/charts/mixed_overview',
    xid: '9001',
    wid: '1',
    plotsData: [
      {
        plotId: 'loss_kpi',
        type: 'SUMMARY_BOX',
        summaryData: {
          metricName: 'train/loss',
          latestValue: 0.65,
          meanValue: 0.73,
        },
      },
      {
        plotId: 'loss_curve',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'eval/loss',
          points: [
            { step: '0', avgValue: 1.85 },
            { step: '50', avgValue: 1.45 },
          ],
        },
      },
    ],
  };

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj('DashboardService', ['getChartData']);
    dashboardServiceSpy.getChartData.and.returnValue(of(mockChartData));

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: DashboardService, useValue: dashboardServiceSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(ChartCardHarness);
  });

  it('should render header with formatted experiment title', async () => {
    expect(await harness.getHeaderTitle()).toBe('XID/234911122 - VCC Baseline - WorkUnitID 1');
  });

  it('should fetch chart data on init', () => {
    expect(dashboardServiceSpy.getChartData).toHaveBeenCalledWith({
      name: 'experiments/9001/charts/mixed_overview',
    });
  });

  it('should render child summary box and plot card', async () => {
    expect(await harness.getSummaryBoxCount()).toBe(1);
    expect(await harness.getPlotCardCount()).toBe(2);
  });
});
